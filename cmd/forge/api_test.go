package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeUnit writes a systemd unit into a sandboxed HOME's user-unit directory.
// forge resolves unit paths through os.UserHomeDir, which honours $HOME, so
// t.Setenv("HOME", …) is enough to keep these tests off the real units — which
// matters more than usual here, because the write path this file also covers
// rewrites unit files and restarts services.
func writeUnit(t *testing.T, home, service, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config/systemd/user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, service+".service"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func routeFor(t *testing.T, routes []routeInfo, service, envVar string) routeInfo {
	t.Helper()
	for _, r := range routes {
		if r.Service == service && r.EnvVar == envVar {
			return r
		}
	}
	t.Fatalf("no route reported for %s/%s", service, envVar)
	return routeInfo{}
}

func TestParseSystemdEnvReturnsTheReadError(t *testing.T) {
	_, err := parseSystemdEnv(filepath.Join(t.TempDir(), "absent.service"))
	if err == nil {
		t.Fatal("parseSystemdEnv swallowed the error for a missing unit file; " +
			"an unreadable unit must not be reported as a unit that declares nothing")
	}
}

// The bug this fixes: an undeclared var was filled in from a hardcoded table, so
// a var no unit sets was indistinguishable from one pinned to the fallback, and
// the API served the guess as configuration. On the live box the real units
// declared exactly one of the seven routable vars — the other six values this
// endpoint returned were invented.
func TestGetRoutesDoesNotInventValuesForUndeclaredVars(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// si declares one of its two routable vars; kayushkin's unit declares none of
	// its five. Every value forge cannot read from a unit must come back empty.
	writeUnit(t, home, "si", "[Service]\nEnvironment=SI_FEED=nats\nEnvironment=LOGSTACK_URL=http://127.0.0.1:8088\n")
	writeUnit(t, home, "kayushkin", "[Service]\nEnvironment=PORT=8080\n")

	routes := getRoutes()

	declared := routeFor(t, routes, "si", "LOGSTACK_URL")
	if declared.Value != "http://127.0.0.1:8088" || !declared.Declared {
		t.Errorf("a declared var must be reported as declared, with its value: got value=%q declared=%v",
			declared.Value, declared.Declared)
	}

	// These are the values the old fallback table invented. Each must now be empty.
	for _, undeclared := range []struct{ service, envVar, wasFabricatedAs string }{
		{"kayushkin", "SI_WS_URL", "ws://127.0.0.1:8090/ws"},
		{"kayushkin", "LOGSTACK_URL", "http://127.0.0.1:8088"},
		{"kayushkin", "BUS_URL", "http://127.0.0.1:8100"},
		{"kayushkin", "BUS_AGENT_API_URL", "http://127.0.0.1:8101"},
		{"kayushkin", "FORGE_API_URL", "http://127.0.0.1:8150"},
		{"si", "SI_BUS_URL", "http://127.0.0.1:8100"},
	} {
		r := routeFor(t, routes, undeclared.service, undeclared.envVar)
		if r.Value != "" {
			t.Errorf("%s/%s: no unit declares this var, so forge cannot know its value, but it reported %q (the old hardcoded fallback was %q)",
				undeclared.service, undeclared.envVar, r.Value, undeclared.wasFabricatedAs)
		}
		if r.Declared {
			t.Errorf("%s/%s: reported as declared, but its unit does not set it", undeclared.service, undeclared.envVar)
		}
		// The unit itself is readable — it just doesn't set this var. That is a
		// different state from "forge could not read the unit", and the response
		// has to say which one it is.
		if r.SourceError != "" {
			t.Errorf("%s/%s: unit is readable, so source_error must be empty, got %q",
				undeclared.service, undeclared.envVar, r.SourceError)
		}
	}
}

// A unit forge cannot read is not a service that routes nowhere. The old code
// could not tell the difference: both produced an empty map, and both were then
// filled in from the fallback table.
func TestGetRoutesReportsAnUnreadableUnitAsUnknownRatherThanEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// No unit files at all.

	routes := getRoutes()
	if len(routes) == 0 {
		t.Fatal("getRoutes returned nothing; it should still report each routable var, as unknown")
	}
	for _, r := range routes {
		if r.Env != "prod" {
			continue
		}
		if r.SourceError == "" {
			t.Errorf("%s/%s: its unit does not exist, but source_error is empty — the read error was swallowed",
				r.Service, r.EnvVar)
		}
		if r.Value != "" || r.Declared {
			t.Errorf("%s/%s: nothing is known about this var, but forge reported value=%q declared=%v",
				r.Service, r.EnvVar, r.Value, r.Declared)
		}
		if r.Source == "" {
			t.Errorf("%s/%s: must name the unit file it consulted", r.Service, r.EnvVar)
		}
	}
}

// The response order must not depend on Go's map iteration order: this is a list
// an operator reads, and it used to reshuffle between calls.
func TestGetRoutesOrderIsStable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeUnit(t, home, "si", "[Service]\n")
	writeUnit(t, home, "kayushkin", "[Service]\n")

	first := getRoutes()
	for i := 0; i < 20; i++ {
		next := getRoutes()
		if len(next) != len(first) {
			t.Fatalf("route count changed between calls: %d then %d", len(first), len(next))
		}
		for j := range first {
			if next[j].Service != first[j].Service || next[j].EnvVar != first[j].EnvVar {
				t.Fatalf("route order changed between calls at %d: %s/%s then %s/%s",
					j, first[j].Service, first[j].EnvVar, next[j].Service, next[j].EnvVar)
			}
		}
	}
}

// updateRoute rewrites a systemd unit and restarts the service. Every field of
// the request reaches that unit file, so each one is checked against the same
// table the read path serves.
func TestResolveRouteUpdateRejectsRequestsItMustNotActOn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for _, tc := range []struct {
		name string
		req  routeUpdateRequest
	}{
		{
			// The value is written as `Environment=VAR=<value>`. A line break closes
			// that directive and lets the rest of the request body become systemd
			// directives of the caller's choosing — including ExecStart=, which forge
			// then restarts the unit to run.
			name: "a newline in the value can append arbitrary systemd directives",
			req: routeUpdateRequest{Env: "prod", Service: "si", EnvVar: "SI_BUS_URL",
				Value: "http://127.0.0.1:8100\nExecStart=/bin/sh -c id"},
		},
		{
			name: "a carriage return in the value",
			req: routeUpdateRequest{Env: "prod", Service: "si", EnvVar: "SI_BUS_URL",
				Value: "http://127.0.0.1:8100\rExecStart=/bin/sh -c id"},
		},
		{
			// The service name was joined straight onto the user-unit directory.
			name: "a service name that traverses out of the user unit directory",
			req: routeUpdateRequest{Env: "prod", Service: "../../../../etc/systemd/system/kayushkin",
				EnvVar: "SI_WS_URL", Value: "ws://127.0.0.1:8090/ws"},
		},
		{
			name: "a service that is not reroutable at all",
			req:  routeUpdateRequest{Env: "prod", Service: "bus", EnvVar: "BUS_URL", Value: "http://127.0.0.1:8100"},
		},
		{
			// A rerouting UI reroutes. It does not set arbitrary environment.
			name: "an env var that is not routable for that service",
			req:  routeUpdateRequest{Env: "prod", Service: "si", EnvVar: "PATH", Value: "/tmp/evil"},
		},
		{
			name: "an env var routable for a different service",
			req:  routeUpdateRequest{Env: "prod", Service: "si", EnvVar: "FORGE_API_URL", Value: "http://127.0.0.1:8150"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unit, err := resolveRouteUpdate(tc.req)
			if err == nil {
				t.Fatalf("accepted a request it must refuse, and would have rewritten and restarted %s", unit)
			}
		})
	}
}

func TestResolveRouteUpdateAcceptsARoutableVar(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	unit, err := resolveRouteUpdate(routeUpdateRequest{
		Env: "prod", Service: "si", EnvVar: "SI_BUS_URL", Value: "http://127.0.0.1:8100",
	})
	if err != nil {
		t.Fatalf("refused a routable var: %v", err)
	}
	want := filepath.Join(home, ".config/systemd/user", "si.service")
	if unit != want {
		t.Errorf("resolved to %q, want %q", unit, want)
	}
	// The write path must resolve to the same unit the read path reported, or the
	// UI shows one file and edits another.
	if got := routeFor(t, getRoutes(), "si", "SI_BUS_URL").Source; got != unit {
		t.Errorf("read path reports source %q but write path resolved %q", got, unit)
	}
}

// Every var the read path offers as routable must be one the write path accepts,
// or the UI offers a reroute it will then refuse.
func TestEveryReportedProdRouteIsAcceptedByTheWritePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, r := range getRoutes() {
		if r.Env != "prod" {
			continue
		}
		if _, err := resolveRouteUpdate(routeUpdateRequest{
			Env: "prod", Service: r.Service, EnvVar: r.EnvVar, Value: "http://127.0.0.1:1",
		}); err != nil {
			t.Errorf("%s/%s is served as a routable prod route but the write path refuses it: %v",
				r.Service, r.EnvVar, err)
		}
	}
}

func TestParseSystemdEnvReadsDeclaredVars(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "x.service")
	body := strings.Join([]string{
		"[Service]",
		"Environment=LOGSTACK_URL=http://127.0.0.1:8088",
		"Environment=EMPTY=",
		"ExecStart=/usr/bin/true",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	env, err := parseSystemdEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if env["LOGSTACK_URL"] != "http://127.0.0.1:8088" {
		t.Errorf("LOGSTACK_URL = %q", env["LOGSTACK_URL"])
	}
	// A var declared as empty is still declared — that is a deliberate pin, and it
	// is exactly the case the old fallback table overwrote with a guess.
	if v, ok := env["EMPTY"]; !ok || v != "" {
		t.Errorf("a var declared with an empty value must be present and empty: got %q, present=%v", v, ok)
	}
}
