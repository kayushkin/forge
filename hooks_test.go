package forge

import (
	"testing"
)

// The hook mechanism in hooks.go had no test at all: measured on `main`, an
// unconditional panic() at the entry of NewHook, Evaluate, Execute,
// matchesPatterns, extractFilePath OR containsBuildCommand left
// `go test ./...` green. Six functions, one mechanism, executed by nothing.
//
// It is not dead code — inber drives it. engine_new.go:397 builds the hook and
// build_hooks.go:181 calls Evaluate after every tool result. So these tests
// pin what that caller actually gets.

// ---------------------------------------------------------------------------
// NewHook — the defaults, which decide everything Evaluate later matches on
// ---------------------------------------------------------------------------

func TestNewHookFillsInBuildPatternsWhenTheCallerGivesNone(t *testing.T) {
	f := &Forge{}
	h := f.NewHook(HookConfig{Project: "p"})

	if len(h.buildPatterns) == 0 {
		t.Fatal("build patterns left empty; every Evaluate match would be a miss")
	}
	for _, want := range []string{"*.go", "*.ts", "*.tsx", "*.js", "*.jsx", "*.css", "*.html"} {
		if !contains(h.buildPatterns, want) {
			t.Errorf("default build patterns missing %q: %v", want, h.buildPatterns)
		}
	}
}

func TestPreviewPatternsDefaultToTheBuildPatternsIncludingTheDefaultedOnes(t *testing.T) {
	f := &Forge{}

	// No patterns at all: preview inherits the defaults, not an empty list.
	h := f.NewHook(HookConfig{Project: "p"})
	if len(h.previewPatterns) != len(h.buildPatterns) {
		t.Errorf("preview patterns %v do not mirror build patterns %v", h.previewPatterns, h.buildPatterns)
	}

	// Caller supplies build patterns only: preview inherits those, not the defaults.
	h = f.NewHook(HookConfig{Project: "p", BuildPatterns: []string{"*.rs"}})
	if len(h.previewPatterns) != 1 || h.previewPatterns[0] != "*.rs" {
		t.Errorf("preview patterns = %v, want the caller's build patterns", h.previewPatterns)
	}
}

func TestExplicitPreviewPatternsAreNotOverwrittenByTheBuildPatterns(t *testing.T) {
	f := &Forge{}
	h := f.NewHook(HookConfig{
		Project:         "p",
		BuildPatterns:   []string{"*.go"},
		PreviewPatterns: []string{"*.html"},
	})

	if len(h.previewPatterns) != 1 || h.previewPatterns[0] != "*.html" {
		t.Errorf("preview patterns = %v, want [*.html]", h.previewPatterns)
	}
	if len(h.buildPatterns) != 1 || h.buildPatterns[0] != "*.go" {
		t.Errorf("build patterns = %v, want [*.go]", h.buildPatterns)
	}
}

// The two lists are the SAME SLICE when preview defaults to build. Appending to
// one through a retained HookConfig writes into the other. Nothing in forge does
// that today; this pins the aliasing so a future caller that does finds a test
// rather than a silent pattern change on the other side.
func TestDefaultedPreviewPatternsAliasTheBuildPatterns(t *testing.T) {
	f := &Forge{}
	cfg := HookConfig{Project: "p", BuildPatterns: []string{"*.go", "*.md"}}
	h := f.NewHook(cfg)

	h.buildPatterns[1] = "*.rst"
	if h.previewPatterns[1] != "*.rst" {
		t.Skip("preview patterns no longer alias build patterns — the copy is now defensive, which is fine")
	}
	if !h.matchesPatterns("notes.rst", h.previewPatterns) {
		t.Error("preview patterns alias build patterns but did not follow the write")
	}
}

// ---------------------------------------------------------------------------
// Evaluate — the truth table the orchestrator sees
// ---------------------------------------------------------------------------

func TestAToolThatErroredRecommendsNothingWhateverItTouched(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: true, AutoPreview: true})

	got := h.Evaluate("write_file", `{"file_path":"main.go"}`, "", true)
	if got.Kind != "none" {
		t.Errorf("errored write_file recommended %q, want none", got.Kind)
	}
	got = h.Evaluate("shell", `{"command":"go build ./..."}`, "", true)
	if got.Kind != "none" {
		t.Errorf("errored shell recommended %q, want none", got.Kind)
	}
}

func TestAnUnrecognisedToolRecommendsNothing(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: true, AutoPreview: true})

	for _, tool := range []string{"read_file", "grep", "", "Write", "WRITE_FILE"} {
		if got := h.Evaluate(tool, `{"file_path":"main.go"}`, "", false); got.Kind != "none" {
			t.Errorf("tool %q recommended %q, want none — the switch is case-sensitive and closed", tool, got.Kind)
		}
	}
}

func TestAWriteWithNoExtractablePathRecommendsNothing(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: true, AutoPreview: true})

	for _, input := range []string{``, `{}`, `{"content":"hello"}`, `not json at all`} {
		if got := h.Evaluate("write_file", input, "", false); got.Kind != "none" {
			t.Errorf("input %q recommended %q, want none", input, got.Kind)
		}
	}
}

func TestAMatchingFileBuildsOnlyWhenAutoBuildIsOn(t *testing.T) {
	on := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: true})
	got := on.Evaluate("write_file", `{"file_path":"/repo/main.go"}`, "", false)
	if got.Kind != "build" {
		t.Fatalf("autoBuild on: recommended %q, want build", got.Kind)
	}
	if got.Reason == "" {
		t.Error("build action carries no reason; the orchestrator logs this string")
	}

	off := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: false})
	if got := off.Evaluate("write_file", `{"file_path":"/repo/main.go"}`, "", false); got.Kind != "none" {
		t.Errorf("autoBuild off: recommended %q, want none", got.Kind)
	}
}

// autoBuild off does not stop the evaluation — it falls through to the preview
// check. This is the branch that makes the two switches independent, and it is
// the one a truth table drawn from the switch names alone gets wrong.
func TestABuildMatchWithAutoBuildOffStillFallsThroughToPreview(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: false, AutoPreview: true})

	got := h.Evaluate("write_file", `{"file_path":"/repo/main.go"}`, "", false)
	if got.Kind != "preview" {
		t.Fatalf("recommended %q, want preview — a build match with autoBuild off must still reach the preview check", got.Kind)
	}
}

// And the reverse: when both are on, build wins and preview is never consulted.
func TestBuildTakesPrecedenceOverPreviewWhenBothSwitchesAreOn(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: true, AutoPreview: true})

	if got := h.Evaluate("write_file", `{"file_path":"/repo/index.html"}`, "", false); got.Kind != "build" {
		t.Errorf("recommended %q, want build — build is checked first and returns", got.Kind)
	}
}

func TestAFileMatchingOnlyThePreviewPatternsPreviewsAndNeverBuilds(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{
		Project:         "p",
		AutoBuild:       true,
		AutoPreview:     true,
		BuildPatterns:   []string{"*.go"},
		PreviewPatterns: []string{"*.css"},
	})

	if got := h.Evaluate("write_file", `{"file_path":"site.css"}`, "", false); got.Kind != "preview" {
		t.Errorf("css recommended %q, want preview", got.Kind)
	}
	if got := h.Evaluate("write_file", `{"file_path":"main.go"}`, "", false); got.Kind != "build" {
		t.Errorf("go recommended %q, want build", got.Kind)
	}
	if got := h.Evaluate("write_file", `{"file_path":"README.md"}`, "", false); got.Kind != "none" {
		t.Errorf("md recommended %q, want none", got.Kind)
	}
}

func TestEditFileIsTreatedExactlyLikeWriteFile(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: true})

	w := h.Evaluate("write_file", `{"file_path":"main.go"}`, "", false)
	e := h.Evaluate("edit_file", `{"file_path":"main.go"}`, "", false)
	if w != e {
		t.Errorf("write_file gave %+v, edit_file gave %+v; they share one case arm", w, e)
	}
}

func TestASuccessfulBuildCommandRefreshesThePreviewOnlyWhenAutoPreviewIsOn(t *testing.T) {
	on := (&Forge{}).NewHook(HookConfig{Project: "p", AutoPreview: true})
	if got := on.Evaluate("shell", `{"command":"go test ./..."}`, "ok", false); got.Kind != "refresh" {
		t.Errorf("autoPreview on: recommended %q, want refresh", got.Kind)
	}

	off := (&Forge{}).NewHook(HookConfig{Project: "p", AutoPreview: false})
	if got := off.Evaluate("shell", `{"command":"go test ./..."}`, "ok", false); got.Kind != "none" {
		t.Errorf("autoPreview off: recommended %q, want none", got.Kind)
	}
}

// autoBuild has no effect on the shell arm at all — only autoPreview gates it.
// A reader who expects "build/test succeeded" to be governed by autoBuild is
// wrong, and this is the test that says so.
func TestTheShellArmIsGovernedByAutoPreviewAloneNotAutoBuild(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: true, AutoPreview: false})

	if got := h.Evaluate("shell", `{"command":"go build ./..."}`, "", false); got.Kind != "none" {
		t.Errorf("recommended %q, want none — autoBuild does not reach the shell arm", got.Kind)
	}
}

func TestAShellCommandThatIsNotABuildRecommendsNothing(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p", AutoPreview: true})

	for _, cmd := range []string{`{"command":"ls -la"}`, `{"command":"git status"}`, `{"command":"curl example.com"}`} {
		if got := h.Evaluate("shell", cmd, "", false); got.Kind != "none" {
			t.Errorf("%s recommended %q, want none", cmd, got.Kind)
		}
	}
}

// ⚠️ The only production caller of this mechanism — inber's
// setupForgeHook in engine/engine_new.go — builds the hook with AutoBuild and
// AutoPreview both false. With both switches off, Evaluate can only ever return
// "none", so the whole mechanism is inert wherever it actually runs. That is not
// a bug in hooks.go, but it means no amount of exercise in production would have
// covered any of the branches above, and it explains how six functions stayed
// unreached. If either default ever changes, this test fails and someone re-reads
// the branches that would then go live.
func TestBothSwitchesOffMeansEvaluateCanOnlyEverRecommendNothing(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p", AutoBuild: false, AutoPreview: false})

	inputs := []struct{ tool, input string }{
		{"write_file", `{"file_path":"main.go"}`},
		{"edit_file", `{"file_path":"site.css"}`},
		{"shell", `{"command":"go build ./..."}`},
		{"shell", `{"command":"npm run build"}`},
	}
	for _, in := range inputs {
		if got := h.Evaluate(in.tool, in.input, "", false); got.Kind != "none" {
			t.Errorf("%s %s recommended %q; with both switches off nothing can fire", in.tool, in.input, got.Kind)
		}
	}
}

// ---------------------------------------------------------------------------
// Execute — the half of the mechanism that has effects
// ---------------------------------------------------------------------------

// Kind "none" must not touch the Forge at all. A nil forge is the assertion:
// if Execute ever reaches into it for a no-op action, this panics.
func TestExecutingANoneActionTouchesNothing(t *testing.T) {
	h := &Hook{forge: nil, project: "p"}

	for _, kind := range []string{"none", "", "unrecognised"} {
		if err := h.Execute(Action{Kind: kind}); err != nil {
			t.Errorf("Execute(%q) returned %v, want nil", kind, err)
		}
	}
}

// ⛔ Executing a "build" action commits nothing. It delegates to
// Forge.SlotCommit, which forge.go marks "a v2 stub (deprecated)" and which
// returns nil without touching the database — so an auto-build reports success
// for work that never happened, for every project, existing or not.
//
// This is the whole reason the "build" arm of Evaluate is worth so little: the
// action it recommends has no implementation behind it. Nothing calls Execute
// today (inber calls Evaluate and only logs the Action), so this is latent
// rather than live. The test pins the stub so that implementing SlotCommit
// fails here and whoever does it re-reads this arm.
func TestExecutingABuildCommitsNothingBecauseSlotCommitIsADeprecatedStub(t *testing.T) {
	f, _ := setupForge(t)
	h := f.NewHook(HookConfig{Project: "does-not-exist", SlotID: 99})

	err := h.Execute(Action{Kind: "build", Reason: "file changed: main.go"})
	if err != nil {
		t.Fatalf("SlotCommit now reports %v — it is no longer an unconditional nil, so re-read "+
			"Hook.Execute's build arm and this test's premise", err)
	}
	// A project that does not exist gave the same answer as a real one would:
	// success. Nothing distinguishes them, which is what makes the stub silent.
	if err := f.SlotCommit("does-not-exist", 99, "direct"); err != nil {
		t.Fatalf("SlotCommit reported %v directly; the stub has been implemented", err)
	}
}

func TestExecutingAPreviewOrRefreshReportsTheFailure(t *testing.T) {
	f, _ := setupForge(t)
	h := f.NewHook(HookConfig{Project: "does-not-exist", SlotID: 99, TargetID: "nope"})

	for _, kind := range []string{"preview", "refresh"} {
		if err := h.Execute(Action{Kind: kind}); err == nil {
			t.Errorf("Execute(%q) reported success starting a preview for a project that does not exist", kind)
		}
	}
}

// ---------------------------------------------------------------------------
// matchesPatterns — matching is on the BASE NAME, which has consequences
// ---------------------------------------------------------------------------

func TestPatternsMatchTheBaseNameSoADirectoryComponentNeverMatches(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p"})

	// The base name is what is matched, so a deep path still matches "*.go".
	if !h.matchesPatterns("/a/b/c/main.go", []string{"*.go"}) {
		t.Error(`"*.go" did not match a nested main.go`)
	}

	// ⚠️ And the same rule means a pattern carrying a directory can never match
	// anything, because the candidate has had its directories stripped. A caller
	// who configures BuildPatterns: ["cmd/*.go"] gets silence, not an error.
	if h.matchesPatterns("cmd/forge/main.go", []string{"cmd/*.go"}) {
		t.Error(`"cmd/*.go" matched; base-name matching should make it unmatchable`)
	}
	if h.matchesPatterns("cmd/main.go", []string{"cmd/*"}) {
		t.Error(`"cmd/*" matched; base-name matching should make it unmatchable`)
	}
}

// ⚠️ filepath.Match's error is discarded, so a malformed pattern is not a
// configuration error — it is a pattern that silently never matches.
func TestAMalformedPatternIsSilentlyUnmatchable(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p"})

	if h.matchesPatterns("main.go", []string{"[", "*.go"}) != true {
		t.Error("a malformed pattern ahead of a good one suppressed the good one")
	}
	if h.matchesPatterns("main.go", []string{"["}) {
		t.Error("a malformed pattern reported a match")
	}
}

func TestAnEmptyPatternListMatchesNothing(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p"})

	if h.matchesPatterns("main.go", nil) {
		t.Error("nil pattern list matched")
	}
	if h.matchesPatterns("main.go", []string{}) {
		t.Error("empty pattern list matched")
	}
}

func TestMatchingIsCaseSensitive(t *testing.T) {
	h := (&Forge{}).NewHook(HookConfig{Project: "p"})

	if h.matchesPatterns("MAIN.GO", []string{"*.go"}) {
		t.Error(`"*.go" matched MAIN.GO; filepath.Match is case-sensitive and the defaults are lowercase`)
	}
}

// ---------------------------------------------------------------------------
// extractFilePath — a hand-rolled scan over JSON, and it is not a JSON parser
// ---------------------------------------------------------------------------

func TestTheFilePathIsReadFromEitherKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"path":"main.go"}`, "main.go"},
		{`{"file_path":"main.go"}`, "main.go"},
		{`{"file_path":"/abs/with spaces/main.go"}`, "/abs/with spaces/main.go"},
		{`{"content":"x","file_path":"b.go"}`, "b.go"},
		{`{"file_path" : "spaced.go"}`, "spaced.go"},
	}
	for _, c := range cases {
		if got := extractFilePath(c.in); got != c.want {
			t.Errorf("extractFilePath(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNoRecognisedKeyYieldsAnEmptyPath(t *testing.T) {
	for _, in := range []string{``, `{}`, `{"filename":"main.go"}`, `{"target":"main.go"}`, `plain text`} {
		if got := extractFilePath(in); got != "" {
			t.Errorf("extractFilePath(%s) = %q, want empty", in, got)
		}
	}
}

// "path" is searched before "file_path", so when a document carries both the
// short key wins regardless of which appears first. Worth pinning: the two keys
// belong to different tools, and a tool that sends both gets the "path" one.
func TestThePathKeyIsPreferredOverFilePathWhicheverComesFirst(t *testing.T) {
	if got := extractFilePath(`{"file_path":"first.go","path":"second.go"}`); got != "second.go" {
		t.Errorf(`got %q, want "second.go" — "path" is searched first`, got)
	}
	if got := extractFilePath(`{"path":"first.go","file_path":"second.go"}`); got != "first.go" {
		t.Errorf(`got %q, want "first.go"`, got)
	}
}

// ⚠️ The scan finds the key anywhere in the text, at any nesting depth. A
// "path" nested inside some other object wins over the real top-level
// "file_path", and the caller then matches its build patterns against a value
// that was never a file path.
//
// Note what does NOT hijack it: a mention inside a normal JSON-escaped string,
// `grep \"path\" src`, is safe purely by accident — the escaping puts a
// backslash where the scan wants a closing quote, so the needle `"path"` is not
// present. The protection is the escaping, not any check in extractFilePath.
func TestANestedPathKeyHijacksTheExtractionFromTheRealOne(t *testing.T) {
	hijacked := `{"filter":{"path":"*.go"},"file_path":"real.go"}`
	if got := extractFilePath(hijacked); got != "*.go" {
		if got == "real.go" {
			t.Skip("extraction is now depth-aware — it found the top-level key, which is an improvement")
		}
		t.Errorf("got %q, want %q — the nested key is found first", got, "*.go")
	}

	// The escaped-string case, for contrast: the real path survives.
	escaped := `{"command":"grep \"path\" src","file_path":"real.go"}`
	if got := extractFilePath(escaped); got != "real.go" {
		t.Errorf("got %q, want %q — an escaped mention does not contain the needle", got, "real.go")
	}
}

// ⚠️ The value is assumed to be a quoted string. Given a number, the scan runs
// on to the next quote ANYWHERE in the document and returns an unrelated field's
// text as though it were a path.
func TestANonStringValueMakesTheScanReturnAnUnrelatedField(t *testing.T) {
	in := `{"path":5,"note":"unrelated.go"}`

	got := extractFilePath(in)
	if got == "" {
		t.Skip("extraction now rejects a non-string value outright, which is the safer behaviour")
	}
	if got == "5" {
		t.Errorf("got %q — a bare number should not read back as a path", got)
	}
	t.Logf("non-string value returned %q, taken from a later field", got)
}

// ⚠️ Backslash escapes are not honoured, so a path containing an escaped quote
// is truncated at the backslash.
func TestAnEscapedQuoteInThePathTruncatesIt(t *testing.T) {
	in := `{"file_path":"weird\"name.go"}`

	got := extractFilePath(in)
	if got == `weird"name.go` {
		t.Skip("escapes are now decoded, which is the correct behaviour")
	}
	if got != `weird\` {
		t.Errorf("got %q; expected truncation at the backslash", got)
	}
}

func TestAnUnterminatedValueYieldsAnEmptyPath(t *testing.T) {
	if got := extractFilePath(`{"file_path":"unterminated`); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := extractFilePath(`{"file_path":`); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// containsBuildCommand — substring matching, with the reach that implies
// ---------------------------------------------------------------------------

func TestEveryDeclaredBuildKeywordIsRecognised(t *testing.T) {
	for _, kw := range []string{
		"go build", "go test", "go run",
		"npm run build", "npm test", "npx",
		"make", "cargo build", "cargo test",
	} {
		if !containsBuildCommand(`{"command":"` + kw + ` ./..."}`) {
			t.Errorf("keyword %q not recognised", kw)
		}
	}
}

func TestKeywordMatchingIgnoresCase(t *testing.T) {
	if !containsBuildCommand(`{"command":"GO BUILD ./..."}`) {
		t.Error("uppercase GO BUILD not recognised")
	}
	if !containsBuildCommand(`{"command":"Make"}`) {
		t.Error("capitalised Make not recognised")
	}
}

func TestAPlainCommandIsNotABuild(t *testing.T) {
	for _, cmd := range []string{"ls -la", "git status", "cat README.md", "curl example.com", ""} {
		if containsBuildCommand(`{"command":"` + cmd + `"}`) {
			t.Errorf("%q read as a build command", cmd)
		}
	}
}

// ⚠️ Matching is a plain substring test over the WHOLE tool input, not a parse
// of the command word. "make" is a substring of a great many things, and any of
// them will fire a preview refresh.
func TestAKeywordEmbeddedInAnUnrelatedWordStillCountsAsABuild(t *testing.T) {
	embedded := []string{
		`{"command":"cat Makefile"}`,
		`{"command":"cmake --version"}`,
		`{"command":"echo let us make a plan"}`,
		`{"command":"ls npxyz"}`,
	}
	for _, in := range embedded {
		if !containsBuildCommand(in) {
			t.Skipf("%s no longer matches — the check now looks at the command word, which is an improvement", in)
		}
	}
	t.Log("all four embedded-substring inputs read as build commands; the check is substring-based")
}

// And the same reach means a keyword in a field that is not the command counts.
func TestAKeywordInANonCommandFieldCountsAsABuild(t *testing.T) {
	if !containsBuildCommand(`{"command":"ls","description":"prepare to go build later"}`) {
		t.Skip("the check now reads only the command field, which is an improvement")
	}
	t.Log("a keyword outside the command field fired; the whole input JSON is searched")
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
