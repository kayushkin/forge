#!/usr/bin/env python3
"""Score forge's tests for the orchestrator hook mechanism by breaking it.

The scoring engine — and the rules it enforces as refusals — lives in
scripts/sabotage.py. This file is only the case list: one edit per mechanism
the suite is meant to pin.

    python3 scripts/sabotage-hooks.py [--diffs] [--crosstable]

⚠️ **This is the engine's EIGHTH copy, and it is the same blob as the other
seven.** md5 `9a81a32e5827b59c1a3093bf88187b17`, taken from git blob
`664f35f475edb9b7d018a28136211bf58a0ff53e`. Diff before editing; a ninth blob
is a fork. Take the blob off the BRANCH, not out of a working tree — those
checkouts sit on whatever branch their last pass left them on, and md5summing
them answers about the wrong commit (221st).

Why this seam is worth a scorer: measured on `main`, a `panic()` on the first
line of `NewHook`, `Evaluate`, `Execute`, `matchesPatterns`, `extractFilePath`
OR `containsBuildCommand` left `go test ./...` green. Six functions, one
mechanism, executed by nothing. The card named only `Evaluate` — the seventh
consecutive row where the census under-described the mechanism.

⚠️ Unlike the 226th's row, the reach guard means something here: forge's root
package HAS test files (`workspace_test.go`, `workspace_record_test.go`,
`workspace_limit_test.go`, `foreign_keys_test.go`), so a green guard is a claim
about the mechanism and not merely about an untested package.

⚠️ **It is not dead code, and a repo-local search says otherwise.** forge has no
caller for `Evaluate`; inber has two — `engine/engine_new.go:397` builds the
hook, `engine/build_hooks.go:181` calls `Evaluate` after every tool result. This
is exactly the card's own warning about under-counting library callers, and this
row is the one it was written about.

📄 **What the tests found that the coverage row did not.**

`Execute`'s "build" arm calls `Forge.SlotCommit`, which `forge.go` marks *"a v2
stub (deprecated)"* and which returns `nil` without touching anything. So an
auto-build reports success for work that never happened, for every project,
existing or not. Latent rather than live — nothing calls `Execute` today; inber
calls `Evaluate` and only logs the `Action`. Pinned so that implementing
`SlotCommit` reddens the suite and whoever does it re-reads this arm.

📄 And the mechanism is inert where it actually runs. `setupForgeHook` builds
the hook with `AutoBuild: false` and `AutoPreview: false`, and with both
switches off `Evaluate` can only ever return `"none"`. That is how six functions
stayed unreached while being called on every tool result in production:
exercised constantly, down one branch, asserting nothing.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from sabotage import REPO, Case, score  # noqa: E402

TARGETS = [REPO / "hooks.go"]
PACKAGES = ["."]

CASES = [
    # ---- NewHook: the defaults decide everything Evaluate later matches on ----
    Case(
        "the default build patterns lose *.go, so a Go file stops being buildable",
        [('cfg.BuildPatterns = []string{"*.go", "*.ts", "*.tsx", "*.js", "*.jsx", "*.css", "*.html"}',
          'cfg.BuildPatterns = []string{"*.ts", "*.tsx", "*.js", "*.jsx", "*.css", "*.html"}')],
    ),
    Case(
        "preview patterns no longer inherit the build patterns, so previews never fire",
        [("\t\tcfg.PreviewPatterns = cfg.BuildPatterns", "\t\tcfg.PreviewPatterns = nil")],
    ),
    Case(
        "an explicit preview list is overwritten by the build list — the caller's choice is discarded",
        [("\tif len(cfg.PreviewPatterns) == 0 {", "\tif len(cfg.PreviewPatterns) >= 0 {")],
    ),

    # ---- Evaluate: the truth table the orchestrator sees ----
    Case(
        "a tool that errored is evaluated anyway, so a failed write recommends a build",
        [("\tif isError {\n\t\treturn Action{Kind: \"none\"}\n\t}",
          "\tif isError && false {\n\t\treturn Action{Kind: \"none\"}\n\t}")],
    ),
    Case(
        "edit_file drops out of the write arm and stops triggering anything",
        [('\tcase "write_file", "edit_file":', '\tcase "write_file":')],
    ),
    Case(
        "an unextractable path no longer short-circuits, so patterns match against the empty string",
        [('\t\tif filePath == "" {', '\t\tif filePath == "\\x00" {')],
    ),
    Case(
        "the autoBuild switch is inverted",
        [("\t\t\tif h.autoBuild {", "\t\t\tif !h.autoBuild {")],
    ),
    Case(
        "the preview check is gated on autoBuild, so the build/preview switches stop being independent",
        [("\t\tif h.matchesPatterns(filePath, h.previewPatterns) && h.autoPreview {",
          "\t\tif h.matchesPatterns(filePath, h.previewPatterns) && h.autoBuild {")],
    ),
    Case(
        "the build action is renamed, so the orchestrator switches on a kind it does not know",
        [('\t\t\t\t\tKind:   "build",', '\t\t\t\t\tKind:   "rebuild",')],
    ),
    Case(
        "the preview action is renamed",
        [('\t\t\t\tKind:   "preview",', '\t\t\t\tKind:   "previews",')],
    ),
    Case(
        "the shell arm's autoPreview gate is inverted",
        [("\t\t\tif h.autoPreview {", "\t\t\tif !h.autoPreview {")],
    ),
    Case(
        "the refresh action is renamed",
        [('\t\t\t\t\tKind:   "refresh",', '\t\t\t\t\tKind:   "reload",')],
    ),

    # ---- Execute: the half with effects ----
    Case(
        "refresh drops out of the preview arm, so a successful build stops refreshing anything",
        [('\tcase "preview", "refresh":', '\tcase "preview":')],
    ),

    # ---- matchesPatterns ----
    Case(
        "matching moves from the base name to the whole path, so *.go stops matching a nested file",
        [("\tbase := filepath.Base(path)", "\tbase := path")],
    ),
    Case(
        "a malformed pattern starts reporting a match instead of being silently unmatchable",
        [("\t\tif matched, _ := filepath.Match(p, base); matched {",
          "\t\tif matched, err := filepath.Match(p, base); matched || err != nil {")],
    ),

    # ---- extractFilePath ----
    Case(
        "the two keys swap precedence, so a payload carrying both resolves to the other file",
        [('\tfor _, key := range []string{`"path"`, `"file_path"`} {',
          '\tfor _, key := range []string{`"file_path"`, `"path"`} {')],
    ),
    Case(
        "the file_path key is dropped, so the commonest tool input stops yielding a path",
        [('\tfor _, key := range []string{`"path"`, `"file_path"`} {',
          '\tfor _, key := range []string{`"path"`} {')],
    ),
    Case(
        "an unterminated value returns the rest of the document instead of nothing",
        [("\t\tendIdx := strings.Index(rest, `\"`)\n\t\tif endIdx < 0 {\n\t\t\tcontinue\n\t\t}\n\t\treturn rest[:endIdx]",
          "\t\tendIdx := strings.Index(rest, `\"`)\n\t\tif endIdx < 0 {\n\t\t\treturn rest\n\t\t}\n\t\treturn rest[:endIdx]")],
    ),

    # ---- containsBuildCommand ----
    Case(
        "npx drops out of the build keywords",
        [('\t\t"npm run build", "npm test", "npx",', '\t\t"npm run build", "npm test",')],
    ),
    Case(
        "the case fold is removed, so an uppercased build command stops being recognised",
        [("\tlower := strings.ToLower(input)", "\tlower := input")],
    ),

    # ---- controls ----
    # Known-positive: the fall-through verdict is what every "recommends
    # nothing" assertion reads. Drifting it to a real action has to redden.
    Case(
        "CONTROL known-positive: the fall-through recommends a build instead of nothing",
        [('\n\treturn Action{Kind: "none"}\n}\n\n// Execute',
          '\n\treturn Action{Kind: "build"}\n}\n\n// Execute')],
    ),
    # Known-negative: the human-readable reason. The suite asserts it is
    # non-empty, because the orchestrator logs it, and never its wording.
    Case(
        "CONTROL known-negative: the build reason is reworded",
        [('Reason: "file changed: " + filePath,', 'Reason: "changed file: " + filePath,')],
        expected_unnoticed="the suite asserts the reason is non-empty, because inber logs it, and never its wording",
    ),

    # ---- declared, with reasons ----
    Case(
        "the build arm stops calling SlotCommit and returns nil directly",
        [('\t\treturn h.forge.SlotCommit(h.project, h.slotID, "auto: "+action.Reason)',
          '\t\t_ = action\n\t\treturn nil')],
        expected_unnoticed=(
            "SlotCommit is a deprecated v2 stub that already returns nil without doing anything, "
            "so calling it and not calling it are indistinguishable from outside — which is the "
            "defect TestExecutingABuildCommitsNothingBecauseSlotCommitIsADeprecatedStub pins"
        ),
    ),
    Case(
        "the preview request stops carrying the target id",
        [("\t\t\tTargetID: h.targetID,", "\t\t\tTargetID: \"\",")],
        expected_unnoticed=(
            "StartPreview fails on the missing project before it ever reads the target, so the "
            "field cannot change the outcome the suite can observe; pinning it needs a fixture "
            "with a real project and target, which is preview.go's seam and not this one"
        ),
    ),
]


if __name__ == "__main__":
    sys.exit(score(TARGETS, PACKAGES, CASES))
