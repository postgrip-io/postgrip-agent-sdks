#!/usr/bin/env python3
"""Fail-loud drift check between this Go protocol package and the
hand-mirrored TypeScript / Python definitions in the SDK monorepo.

The contract being checked: every exported wire-format struct in
types.go has an equivalent type with matching field names in
agent-sdk-typescript/src/types.ts and agent-sdk-python/src/postgrip_agent/types.py.

What we check (deliberately narrow, since the source of truth is Go):

    * Every exported Go struct in types.go (excluding pure-internal
      helpers like requests/responses unique to the runtime API surface)
      appears with the same name in the TS and Python type files.
    * Every JSON-tagged field on those Go structs has a same-name field on
      the TS interface and the Python TypedDict.

agent-sdk-go is checked under a *different* contract (`--local go-sdk`). It
isn't a mirror — it imports this package and re-exports the wire types as
aliases — so field-set comparison doesn't apply to it. What goes wrong there
instead is redefinition: when the SDK's protocol pin predates a type, the SDK
grows its own local copy that compiles cleanly, mirrors nothing, and is
invisible to every check above. That is exactly how agent-sdk-go came to carry
a hand-rolled WorkflowRuntimePayload and a hardcoded TaskTypeWorkflowRuntime
while this file's copy grew an `isolation` field it never saw. So for the Go
SDK we check the one invariant that matters: it must alias what protocol
owns, never redeclare it.

What we *don't* check yet (room for v2):

    * Field types. JSON-shape fidelity (e.g. int vs string) is the most
      common drift class but requires a real cross-language type table.
    * Optional vs required. Same problem.
    * Renames where one side leads. Reported as "missing on side X" — the
      author has to read the diff to understand intent.
    * Endpoint paths. The SDKs' URL literals are checked against the server's
      route table by postgrip-web's CI, which is the only place that can read
      the (private) server source. See postgrip-web scripts/check-agent-sdk-endpoints.mjs.

Usage:

    python3 tools/check_drift.py --monorepo      # check every local package
    python3 tools/check_drift.py                 # check legacy sibling repos
    python3 tools/check_drift.py --from-github   # fetch peers from GitHub
    python3 tools/check_drift.py --from-github --github-ref "$BRANCH"
    python3 tools/check_drift.py --local go-sdk --from-github   # from agent-sdk-go

Exit codes: 0 clean, 1 drift detected, 2 tooling failure.
"""
from __future__ import annotations

import argparse
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Iterable

REPO_ROOT = Path(__file__).resolve().parent.parent
MONOREPO_ROOT = REPO_ROOT.parent


class FetchError(RuntimeError):
    """A peer type file could not be retrieved.

    Distinct from drift on purpose. A GitHub incident used to surface as an
    unhandled HTTPError traceback and exit 1 — the same exit code as real
    drift — so an outage was indistinguishable from a genuine finding. This
    maps to exit 2, tooling failure.
    """


# Wire types we actively contract on. Keep this list narrow on purpose —
# every type added here is a commitment that TS / Python will mirror it.
# Server-only request shapes (e.g. CompactRequest, EnrollAgentRequest) and
# unauthenticated bootstrap shapes don't belong here.
TRACKED_TYPES = [
    "Task",
    "TaskResult",
    "TaskEvent",
    "TaskEventInput",
    "EnqueueTaskRequest",
    "FailureInfo",
    "ContinueAsNewResult",
    "ShellExecPayload",
    "ContainerExecPayload",
    "WorkflowRuntimePayload",
    "TimerPayload",
    "ActivityTaskPayload",
    "WorkflowPayload",
    "WorkflowQueryPayload",
    "WorkflowUpdatePayload",
    "WorkflowExecution",
    "WorkflowHistoryEvent",
    "Schedule",
    "ScheduleSpec",
    "ScheduleAction",
    "ScheduleCalendarSpec",
    "RetryPolicy",
    # Sandbox platform, client-facing shapes only. The agent-plane sandbox
    # types (SandboxObservation, SandboxReconcile*, SandboxSessionAssignment,
    # SandboxEvent) are deliberately absent: they never cross a client SDK, and
    # tracking them would force TS/Python to mirror types nothing there uses —
    # the same reason EnrollAgentRequest and PollTaskResponse aren't listed.
    "Sandbox",
    "SandboxCreateRequest",
    "SandboxListResponse",
    "SandboxResourceLimits",
    "SandboxNetworkPolicy",
    "SandboxPortMapping",
    "SandboxWorkspace",
    "CreateSandboxSessionRequest",
    "CreateSandboxSessionResponse",
]

# Where to fetch type files. The "go" url points at agent-sdk-protocol so the
# script can be run from any of the four repos and pull whichever languages
# aren't on disk. --from-github uses these for everything; otherwise we look
# for sibling working dirs at agent-sdk-{language}/.
#
# Note the asymmetry: Go package files live at module root (idiomatic Go
# layout means consumer imports are
# `github.com/postgrip-io/agent-sdk-protocol`, not `…/src`). TS and Python
# keep `src/` per their idiomatic layouts.
#
# Each language maps to a LIST of files, concatenated before parsing. The
# protocol package is a Go package, not a single file, and pretending
# otherwise silently drops whatever isn't in types.go: adding sandbox.go made
# every type in it report "not found in types.go" while the mirrors sat
# unchecked. A language's declarations may live in as many files as it likes.
GITHUB_SOURCES = {
    "go":     ("postgrip-io", "agent-sdk-protocol", ["types.go", "sandbox.go"]),
    "ts":     ("postgrip-io", "agent-sdk-typescript", ["src/types.ts"]),
    "python": ("postgrip-io", "agent-sdk-python", ["src/postgrip_agent/types.py"]),
}
# Repo-local paths, keyed by --local: the language whose types live in this
# checkout (CI in that repo will set --local to it so a PR's changes are
# checked against the OTHER two languages fetched from github main).
LOCAL_PATHS = {
    "go":     [Path("types.go"), Path("sandbox.go")],
    "ts":     [Path("src/types.ts")],
    "python": [Path("src/postgrip_agent/types.py")],
}
# Sibling working-dir layout for local development across all four repos.
SIBLING_PATHS = {
    "go":     [REPO_ROOT.parent / "agent-sdk-protocol" / "types.go",
               REPO_ROOT.parent / "agent-sdk-protocol" / "sandbox.go"],
    "ts":     [REPO_ROOT.parent / "agent-sdk-typescript" / "src" / "types.ts"],
    "python": [REPO_ROOT.parent / "agent-sdk-python" / "src" / "postgrip_agent" / "types.py"],
}
# Consolidated repository layout. Keeping this explicit makes CI independent
# of GitHub availability and guarantees one pull request is validated as an
# atomic cross-language contract change.
MONOREPO_PATHS = {
    "go":     [MONOREPO_ROOT / "protocol" / "types.go",
               MONOREPO_ROOT / "protocol" / "sandbox.go"],
    "ts":     [MONOREPO_ROOT / "typescript" / "src" / "types.ts"],
    "python": [MONOREPO_ROOT / "python" / "src" / "postgrip_agent" / "types.py"],
}

# json:"..." -> field name (strip ",omitempty" etc.)
JSON_TAG_RE = re.compile(r'json:"([^",]+)')


def go_json_field_names(body: str) -> set[str]:
    """JSON field names declared in a Go struct body.

    `json:"-"` is excluded: the tag means the field never crosses the wire, so
    there is nothing for TS or Python to mirror. Without this the checker
    demands a field literally named "-" in every mirror — which is how a
    server-side-only field on a wire type became impossible to express.
    """
    return {name for name in JSON_TAG_RE.findall(body) if name != "-"}

# Go: type X struct { ... }
GO_STRUCT_RE = re.compile(r'^type\s+(\w+)\s+struct\s*\{', re.MULTILINE)
# Go: type X = Y  (alias)
GO_ALIAS_RE = re.compile(r'^type\s+(\w+)\s*=\s*(\w+)\s*$', re.MULTILINE)
# Go: a const bound to a string literal, typed or untyped, inside a block or
# on its own line:
#   TaskTypeNoop                       = "noop"
#   TaskStateQueued TaskState          = "queued"
#   TaskStateQueued protocol.TaskState = "queued"   <- qualified type
#   const TaskTypeNoop                 = `noop`     <- raw string literal
# The leading keyword is consumed explicitly — without it the single-line form
# captures "const" as the constant's name and the real name goes unchecked.
# The type is `[\w.]+` (not `\w+`) so a qualified type doesn't hide the
# declaration, and both interpreted and raw string literals count: either
# spelling hardcodes a protocol-owned value just as thoroughly.
# Matches assignment to a literal only, so `X = protocol.X` (the correct
# re-export form) is deliberately not a hit.
GO_LITERAL_CONST_RE = re.compile(
    r'^\s*(?:(?:const|var)\s+)?(\w+)(?:\s+[\w.]+)?\s*=\s*["`]', re.MULTILINE,
)

# Directories skipped when scanning the Go SDK tree: generated docs sites and
# vendored dependencies. `vendor/` matters most — a dependency declaring a
# common name like `type Task struct` is not an SDK redeclaration, and without
# this the job is unusable in a vendored checkout.
GO_SDK_SKIP_DIRS = {".git", "site", "docs", "doc", "vendor", "node_modules"}

# One type declaration, as it appears either on its own line (after the `type`
# keyword is stripped) or inside a grouped `type ( ... )` block:
#   Task = protocol.Task     -> alias, target protocol.Task
#   Task protocol.Task       -> DEFINED type: compiles, distinct identity
#   Task struct {            -> local redeclaration
GO_TYPE_DECL_RE = re.compile(r"(\w+)\s*(=)?\s*(.+)")


def parse_go_types(source: str) -> dict[str, set[str]]:
    """Map every `type X struct { ... }` to the set of JSON-tagged field
    names. Type aliases (`type X = Y`) are resolved transparently so callers
    can ask for either side of the alias and get the same field set."""
    structs: dict[str, set[str]] = {}
    pos = 0
    while pos < len(source):
        m = GO_STRUCT_RE.search(source, pos)
        if m is None:
            break
        name = m.group(1)
        i = m.end() - 1
        assert source[i] == "{"
        depth = 1
        i += 1
        while i < len(source) and depth:
            if source[i] == "{":
                depth += 1
            elif source[i] == "}":
                depth -= 1
            i += 1
        body = source[m.end() : i - 1]
        structs[name] = go_json_field_names(body)
        pos = i

    # Resolve aliases by copying the target's field set under the alias name.
    # Aliases of aliases work because we resolve eagerly until the target is
    # a real struct or unresolvable.
    aliases = {m.group(1): m.group(2) for m in GO_ALIAS_RE.finditer(source)}
    out = dict(structs)
    for alias, target in aliases.items():
        seen = {alias}
        cur = target
        while cur in aliases and cur not in seen:
            seen.add(cur)
            cur = aliases[cur]
        if cur in structs:
            out[alias] = structs[cur]
    return out


def repeated(names: Iterable[str]) -> set[str]:
    """Names declared more than once.

    Worth failing on its own: every parser here keys types by name, so a
    second declaration silently replaces the first and only one of them is
    ever compared. TypeScript makes this especially quiet — duplicate
    `export interface` declarations merge instead of erroring, so the file
    compiles and the drift check validates whichever copy came last.
    """
    seen: set[str] = set()
    dupes: set[str] = set()
    for name in names:
        if name in seen:
            dupes.add(name)
        seen.add(name)
    return dupes


def go_struct_names(source: str) -> set[str]:
    """Names declared as `type X struct {`. Unlike parse_go_types this does
    not resolve aliases — the point is to tell a real local declaration apart
    from a re-export."""
    return {m.group(1) for m in GO_STRUCT_RE.finditer(source)}


def go_type_declarations(source: str) -> list[tuple[str, bool, str]]:
    """Every type declaration as (name, is_alias, target).

    Covers both spellings, because the SDK uses both: a grouped
    `type ( X = protocol.X ... )` block for the re-exports, and standalone
    `type X struct {` for anything it declares itself.
    """
    lines: list[str] = []
    for block in re.finditer(r"^type\s*\(\s*$(.*?)^\)", source, re.MULTILINE | re.DOTALL):
        lines.extend(block.group(1).splitlines())
    for m in re.finditer(r"^type\s+(.+)$", source, re.MULTILINE):
        lines.append(m.group(1))

    out: list[tuple[str, bool, str]] = []
    for line in lines:
        line = line.split("//")[0].strip()
        if not line:
            continue
        m = GO_TYPE_DECL_RE.match(line)
        if m:
            out.append((m.group(1), m.group(2) == "=", m.group(3).strip()))
    return out


def go_literal_const_names(source: str) -> set[str]:
    """Names bound to a string literal in a const/var block."""
    return {m.group(1) for m in GO_LITERAL_CONST_RE.finditer(source)}


def iter_go_sdk_sources(root: Path) -> Iterable[Path]:
    for path in sorted(root.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        if any(part in GO_SDK_SKIP_DIRS for part in path.relative_to(root).parts):
            continue
        yield path


def check_go_sdk(root: Path, protocol_source: str) -> list[str]:
    """Fail when the Go SDK redeclares anything the protocol package owns.

    Both halves matter and both have bitten us: a redeclared *struct* silently
    forks the wire shape, and a redeclared *constant* silently forks the
    vocabulary. In each case the SDK keeps compiling against a stale pin
    instead of failing to build, which is what let the divergence live.
    """
    # Ownership is EVERY struct the protocol package declares, not just
    # TRACKED_TYPES. That list is deliberately narrow — it's the set the TS and
    # Python SDKs promise to mirror — and using it here would leave a Go SDK
    # copy of PollTaskResponse, EnrollAgentRequest or AgentSecurityRecord
    # entirely unchecked, despite this mode promising to reject protocol-owned
    # redeclarations.
    #
    # Matched case-insensitively: an unexported copy (`workflowRuntimePayload`)
    # forks the wire shape exactly as an exported one does, and is in fact the
    # likelier form — it's the struct a marshalling helper reaches for.
    owned_types = {
        name.lower(): name
        for name in set(TRACKED_TYPES) | go_struct_names(protocol_source)
    }
    owned_consts = {name.lower(): name for name in go_literal_const_names(protocol_source)}
    failures: list[str] = []
    for path in iter_go_sdk_sources(root):
        try:
            source = path.read_text(encoding="utf-8")
        except OSError as exc:  # unreadable file is a tooling problem, not drift
            failures.append(f"  {path}: could not read ({exc})")
            continue
        rel = path.relative_to(root)
        for declared, is_alias, target in go_type_declarations(source):
            owned = owned_types.get(declared.lower())
            if owned is None:
                continue
            # Only one form is acceptable: an alias to the protocol type of the
            # same name. `type Task protocol.Task` compiles but creates a
            # DISTINCT type, and `type Task = SomethingElse` re-exports the
            # wrong shape — both leave the SDK free to diverge while looking
            # like a re-export.
            if is_alias and target == f"protocol.{owned}":
                continue
            how = f"= {target}" if is_alias else target
            failures.append(
                f"  {rel}: `{declared} {how}` is not an alias of the wire type "
                f"`{owned}` — declare it as `{owned} = protocol.{owned}` and "
                f"bump the agent-sdk-protocol pin if the type is missing there"
            )
        for declared in sorted(go_literal_const_names(source)):
            owned = owned_consts.get(declared.lower())
            if owned is None:
                continue
            failures.append(
                f"  {rel}: redeclares protocol constant `{owned}` as a string "
                f"literal — re-export it instead (`{owned} = protocol.{owned}`)"
            )
    return failures


# TypeScript: export interface X { ... } | export interface X<...> { ... }
TS_INTERFACE_RE = re.compile(
    r'^export\s+interface\s+(\w+)(?:<[^>]*>)?\s*\{', re.MULTILINE,
)
# field name from a TS line like `  foo?: Bar;` or `  foo_bar: Baz;`
TS_FIELD_RE = re.compile(r'^\s*([a-zA-Z_][a-zA-Z0-9_]*)\??:\s', re.MULTILINE)


def parse_ts_types(source: str) -> dict[str, set[str]]:
    out: dict[str, set[str]] = {}
    pos = 0
    while pos < len(source):
        m = TS_INTERFACE_RE.search(source, pos)
        if m is None:
            break
        name = m.group(1)
        i = m.end() - 1
        assert source[i] == "{"
        depth = 1
        i += 1
        while i < len(source) and depth:
            if source[i] == "{":
                depth += 1
            elif source[i] == "}":
                depth -= 1
            i += 1
        body = source[m.end() : i - 1]
        fields = set(TS_FIELD_RE.findall(body))
        out[name] = fields
        pos = i
    return out


# Python: class X(TypedDict, total=False): ... or class X(TypedDict): ...
PY_CLASS_RE = re.compile(
    r'^class\s+(\w+)\s*\(\s*TypedDict[^)]*\)\s*:', re.MULTILINE,
)
# field on a TypedDict body line like `    foo: int` or `    foo_bar: list[str]`
PY_FIELD_RE = re.compile(r'^\s{4}([a-zA-Z_][a-zA-Z0-9_]*)\s*:\s')


def parse_py_types(source: str) -> dict[str, set[str]]:
    out: dict[str, set[str]] = {}
    lines = source.splitlines()
    i = 0
    while i < len(lines):
        m = PY_CLASS_RE.match(lines[i])
        if not m:
            i += 1
            continue
        name = m.group(1)
        fields: set[str] = set()
        i += 1
        # Body lines are indented at least 4 spaces. A blank line is allowed
        # inside the body (but unindented content terminates the class).
        while i < len(lines):
            line = lines[i]
            if not line.strip():
                i += 1
                continue
            if not line.startswith("    "):
                break
            field_match = PY_FIELD_RE.match(line)
            if field_match:
                fields.add(field_match.group(1))
            i += 1
        out[name] = fields
    return out


# Statuses worth retrying rather than reporting. raw.githubusercontent
# rate-limits (429) and sheds load (5xx) during GitHub incidents, and a fetch
# that fails for those reasons says nothing about the type files.
RETRYABLE_HTTP_STATUSES = frozenset({408, 429, 500, 502, 503, 504})
FETCH_ATTEMPTS = 3


def load(path_or_url: str | Path, *, from_github: bool) -> str:
    if not from_github:
        with open(path_or_url, encoding="utf-8") as fh:
            return fh.read()
    last_err: Exception | None = None
    for attempt in range(FETCH_ATTEMPTS):
        try:
            with urllib.request.urlopen(path_or_url, timeout=30) as resp:
                return resp.read().decode("utf-8")
        except urllib.error.HTTPError as exc:
            if exc.code == 404 or exc.code not in RETRYABLE_HTTP_STATUSES:
                raise
            last_err = exc
        except urllib.error.URLError as exc:
            last_err = exc
        if attempt < FETCH_ATTEMPTS - 1:
            time.sleep(0.5 * 2**attempt)
    raise FetchError(f"fetching {path_or_url}: {last_err}") from last_err


def github_raw_url(lang: str, ref: str, path: str) -> str:
    owner, repo, _ = GITHUB_SOURCES[lang]
    return f"https://raw.githubusercontent.com/{owner}/{repo}/{ref}/{path}"


def load_all(paths, *, from_github: bool) -> str:
    """Concatenate every declaration file for one language.

    Parsing the joined text is safe because every parser here is anchored to
    line starts, and it keeps a language free to split its declarations across
    files without the checker quietly ignoring the ones it wasn't told about.
    """
    return "\n".join(load(p, from_github=from_github) for p in paths)


def github_ref_candidates(preferred_ref: str) -> list[str]:
    preferred_ref = preferred_ref.strip() or "main"
    refs = [preferred_ref]
    if preferred_ref != "main":
        refs.append("main")
    return refs


def load_one_from_github(lang: str, path: str, refs: list[str]) -> str:
    """Fetch one declaration file, resolving the ref candidates for it alone.

    The fallback is deliberately per path rather than per language. Resolving it
    for the whole bundle meant that a PR branch adding sandbox.go while leaving
    types.go untouched 404'd on the second URL and dropped *back to main for
    both* — so the branch's real types.go was silently replaced by main's, and
    the cross-repo drift job could pass without ever reading the wire change it
    was triggered for. A missing file on a branch means that one file is
    unchanged there, never that the branch should be abandoned.
    """
    last_err: Exception | None = None
    for ref in refs:
        try:
            return load(github_raw_url(lang, ref, path), from_github=True)
        except urllib.error.HTTPError as exc:
            # Only a 404 means "this ref doesn't have the file" and should fall
            # through to the next candidate ref. Anything else already
            # exhausted its retries in load().
            if exc.code != 404:
                raise FetchError(f"fetching {lang} {path} at ref {ref}: {exc}") from exc
            last_err = exc
    raise FetchError(
        f"could not fetch {lang} file {path} from GitHub refs {', '.join(refs)}"
    ) from last_err


def load_from_github(lang: str, refs: list[str]) -> str:
    _, _, paths = GITHUB_SOURCES[lang]
    return "\n".join(load_one_from_github(lang, p, refs) for p in paths)


def diff_field_sets(
    name: str, lang: str, go_fields: set[str], lang_fields: set[str],
) -> Iterable[str]:
    missing = sorted(go_fields - lang_fields)
    extra = sorted(lang_fields - go_fields)
    if missing:
        yield f"  {name}.{lang}: missing fields present in Go: {', '.join(missing)}"
    if extra:
        yield f"  {name}.{lang}: extra fields not present in Go: {', '.join(extra)}"


SELF_TEST_GO = '''
package protocol

const (
	TaskTypeNoop  = "noop"
	TaskTypeTimer = "timer"
)

type TimerPayload struct {
	TimerID    string `json:"timerId"`
	DurationMs int64  `json:"durationMs,omitempty"`
}
'''

SELF_TEST_TS = '''
export interface TimerPayload {
  timerId: string;
  durationMs?: number;
}
'''

SELF_TEST_PY = '''
class TimerPayload(TypedDict, total=False):
    timerId: str
    durationMs: int
'''

# A Go SDK file doing it right: aliases and re-exports, declares nothing.
# Includes the grouped `type ( ... )` form the SDK actually uses.
SELF_TEST_SDK_GOOD = '''
package client

import "github.com/postgrip-io/agent-sdk-protocol"

type (
	TimerPayload = protocol.TimerPayload
	OwnedElsewhere = protocol.TimerPayload
)

type LocalOptions struct {
	Name string
}

const TaskTypeNoop = protocol.TaskTypeNoop
'''

# ...and the same file gone wrong, in both ways we care about.
SELF_TEST_SDK_BAD = '''
package client

type timerPayload struct {
	TimerID string `json:"timerId"`
}

const TaskTypeNoop = "noop"
'''

# Three shapes that compile, look like re-exports, and are not:
#   defined type   -> distinct identity, free to diverge
#   wrong target   -> re-exports something else entirely
#   raw literal / qualified type -> hardcodes a protocol-owned value
SELF_TEST_SDK_SUBTLE = '''
package client

type TimerPayload protocol.TimerPayload

type (
	TaskResult = LocalTaskResult
)

const TaskTypeTimer protocol.TaskType = `timer`
'''


def self_test() -> int:
    """Exercise every detector against synthetic sources.

    A clean tree only proves today's files are clean; it never proves the
    checker still works. A regex that silently stops matching would turn this
    whole tool into a green light. So assert the detectors both fire and stay
    quiet on the correct form.
    """
    import tempfile

    failures: list[str] = []

    def check(label: str, got, want) -> None:
        if got != want:
            failures.append(f"  {label}: got {got!r}, want {want!r}")

    go_types = parse_go_types(SELF_TEST_GO)
    check("parse_go_types fields", go_types.get("TimerPayload"), {"timerId", "durationMs"})
    check("parse_ts_types fields", parse_ts_types(SELF_TEST_TS).get("TimerPayload"), {"timerId", "durationMs"})
    check("parse_py_types fields", parse_py_types(SELF_TEST_PY).get("TimerPayload"), {"timerId", "durationMs"})

    # Regression: json:"-" was captured as a field named "-", so any wire type
    # carrying a server-side-only field demanded a "-" field in both mirrors.
    check(
        "parse_go_types skips json:\"-\" fields",
        parse_go_types('type X struct {\n\tA string `json:"a"`\n\tB string `json:"-"`\n}').get("X"),
        {"a"},
    )
    check("go_struct_names", go_struct_names(SELF_TEST_GO), {"TimerPayload"})
    check("go_literal_const_names", go_literal_const_names(SELF_TEST_GO), {"TaskTypeNoop", "TaskTypeTimer"})
    # The correct re-export form must NOT register as a literal const.
    check("go_literal_const_names ignores re-exports", go_literal_const_names(SELF_TEST_SDK_GOOD), set())
    # Regression: a qualified type or a raw string literal used to slip past
    # the const detector entirely, so a hardcoded protocol value read clean.
    check(
        "go_literal_const_names sees qualified types and raw literals",
        go_literal_const_names('const TaskTypeTimer protocol.TaskType = `timer`'),
        {"TaskTypeTimer"},
    )
    # Regression: only standalone `type X struct` was parsed, so the grouped
    # `type ( ... )` form the SDK actually uses went entirely unexamined.
    decls = dict((n, (a, t)) for n, a, t in go_type_declarations(SELF_TEST_SDK_GOOD))
    check("go_type_declarations grouped alias", decls.get("TimerPayload"), (True, "protocol.TimerPayload"))
    check("go_type_declarations local struct", decls.get("LocalOptions"), (False, "struct {"))

    check(
        "diff_field_sets missing",
        list(diff_field_sets("X", "ts", {"a", "b"}, {"a"})),
        ["  X.ts: missing fields present in Go: b"],
    )
    check(
        "diff_field_sets extra",
        list(diff_field_sets("X", "ts", {"a"}, {"a", "b"})),
        ["  X.ts: extra fields not present in Go: b"],
    )
    check("diff_field_sets clean", list(diff_field_sets("X", "ts", {"a"}, {"a"})), [])

    # A fetch failure must reach the handlers that map to exit 2 (tooling),
    # not fall through as an unhandled traceback that exits 1 and reads as
    # drift. Both call sites catch RuntimeError, so the subclassing is the
    # contract — a GitHub 429 during an incident used to look like a finding.
    check("FetchError is caught as a tooling failure", issubclass(FetchError, RuntimeError), True)
    check("retryable statuses include rate limiting", 429 in RETRYABLE_HTTP_STATUSES, True)
    check("404 is not retried", 404 in RETRYABLE_HTTP_STATUSES, False)

    check("repeated finds duplicates", repeated(["a", "b", "a"]), {"a"})
    check("repeated on unique input", repeated(["a", "b"]), set())
    check(
        "repeated over TS interfaces",
        repeated(m.group(1) for m in TS_INTERFACE_RE.finditer(SELF_TEST_TS + SELF_TEST_TS)),
        {"TimerPayload"},
    )

    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        (root / "good.go").write_text(SELF_TEST_SDK_GOOD, encoding="utf-8")
        check("check_go_sdk accepts aliases", check_go_sdk(root, SELF_TEST_GO), [])
        (root / "bad.go").write_text(SELF_TEST_SDK_BAD, encoding="utf-8")
        bad = check_go_sdk(root, SELF_TEST_GO)
        # Two distinct findings: the unexported struct copy and the literal const.
        if len(bad) != 2:
            failures.append(f"  check_go_sdk redeclaration: got {len(bad)} findings, want 2: {bad}")
        elif "timerPayload" not in bad[0] or "TaskTypeNoop" not in bad[1]:
            failures.append(f"  check_go_sdk redeclaration: unexpected findings: {bad}")
        # _test.go files are skipped on purpose; prove the skip still holds.
        (root / "fixture_test.go").write_text(SELF_TEST_SDK_BAD, encoding="utf-8")
        if len(check_go_sdk(root, SELF_TEST_GO)) != 2:
            failures.append("  check_go_sdk: _test.go files are no longer skipped")
        # Regression: vendored dependencies were scanned despite the comment
        # claiming otherwise, so any dep declaring `type Task struct` made the
        # job unusable in a vendored checkout.
        (root / "vendor").mkdir()
        (root / "vendor" / "dep.go").write_text(SELF_TEST_SDK_BAD, encoding="utf-8")
        if len(check_go_sdk(root, SELF_TEST_GO)) != 2:
            failures.append("  check_go_sdk: vendor/ is no longer skipped")

    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        # Regression: three shapes that compile and read as re-exports but
        # aren't — a defined type (distinct identity), an alias to the wrong
        # target, and a raw-literal constant with a qualified type. All three
        # used to pass clean.
        (root / "subtle.go").write_text(SELF_TEST_SDK_SUBTLE, encoding="utf-8")
        subtle = check_go_sdk(root, SELF_TEST_GO)
        if len(subtle) != 3:
            failures.append(f"  check_go_sdk subtle forms: got {len(subtle)} findings, want 3: {subtle}")
        # Regression: ownership came from TRACKED_TYPES, so a copy of any
        # protocol struct outside that narrow list went unchecked.
        (root / "wide.go").write_text(
            "package client\n\ntype PollTaskResponse struct {\n\tTask string\n}\n", encoding="utf-8"
        )
        wide = check_go_sdk(root, "type PollTaskResponse struct {\n\tTask *Task `json:\"task\"`\n}\n")
        if not any("PollTaskResponse" in f for f in wide):
            failures.append(f"  check_go_sdk ownership beyond TRACKED_TYPES: got {wide}")

    # Regression: the preferred/main fallback was resolved once for the whole
    # language bundle. A PR branch that changed sandbox.go but not types.go
    # 404'd on types.go and dropped back to main for *both* files, so the job
    # checked main's sandbox.go — never the wire change it was triggered for,
    # and reported success. Each path must resolve its own ref.
    fetched: list[str] = []

    def fake_load(url, *, from_github):
        fetched.append(url.rsplit("/agent-sdk-protocol/", 1)[-1])
        if url.endswith("/branch/types.go"):
            raise urllib.error.HTTPError(url, 404, "Not Found", None, None)
        return f"// {url}\n"

    real_load = globals()["load"]
    globals()["load"] = fake_load
    try:
        joined = load_from_github("go", ["branch", "main"])
    finally:
        globals()["load"] = real_load
    check(
        "load_from_github resolves the ref per path",
        fetched,
        ["branch/types.go", "main/types.go", "branch/sandbox.go"],
    )
    # And the branch's own file is what lands in the parsed text, not main's.
    check("load_from_github keeps the branch file", "branch/sandbox.go" in joined, True)

    if failures:
        print("check_drift self-test FAILED:", file=sys.stderr)
        for line in failures:
            print(line, file=sys.stderr)
        return 2
    print("check_drift self-test OK.")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument(
        "--self-test",
        action="store_true",
        help="exercise the detectors against synthetic sources and exit; run this before the tree scan so a clean tree can't pass on a broken checker.",
    )
    ap.add_argument(
        "--monorepo",
        action="store_true",
        help="read protocol, Go SDK, TypeScript, and Python sources from the consolidated repository and validate them together",
    )
    ap.add_argument(
        "--from-github",
        action="store_true",
        help="fetch all three language type files from main on github (skip sibling working dirs)",
    )
    ap.add_argument(
        "--local",
        choices=("go", "ts", "python", "go-sdk"),
        help="language whose type file should be read from this checkout (typically set by CI in the SDK repo of that language). Other two are fetched per --from-github / sibling-working-dir. `go-sdk` runs the agent-sdk-go redeclaration check instead of the mirror comparison.",
    )
    ap.add_argument(
        "--github-ref",
        default=os.environ.get("POSTGRIP_AGENT_SDK_DRIFT_REF")
        or os.environ.get("GITHUB_HEAD_REF")
        or os.environ.get("GITHUB_REF_NAME")
        or "main",
        help="preferred GitHub ref for --from-github peers; falls back to main when the ref is missing in a peer repo.",
    )
    args = ap.parse_args()

    if args.self_test:
        return self_test()

    if args.monorepo and (args.from_github or args.local):
        print(
            "check_drift: --monorepo cannot be combined with --from-github or --local",
            file=sys.stderr,
        )
        return 2

    sources: dict[str, str] = {}
    github_refs = github_ref_candidates(args.github_ref)

    if args.local == "go-sdk":
        # Only protocol's types.go is needed: it supplies the set of names the
        # Go SDK is forbidden to redeclare.
        try:
            if args.from_github:
                protocol_source = load_from_github("go", github_refs)
            else:
                protocol_source = load_all(SIBLING_PATHS["go"], from_github=False)
        except (RuntimeError, FileNotFoundError) as e:
            print(f"check_drift: {e}", file=sys.stderr)
            return 2
        failures = check_go_sdk(REPO_ROOT, protocol_source)
        if failures:
            print("Drift detected in agent-sdk-go:", file=sys.stderr)
            for line in failures:
                print(line, file=sys.stderr)
            print(file=sys.stderr)
            print(
                "The Go SDK must alias the protocol package, never redeclare "
                "it. A local copy compiles against a stale pin instead of "
                "failing to build, which is how wire drift goes unnoticed.",
                file=sys.stderr,
            )
            return 1
        print("Drift check OK (agent-sdk-go redeclares no protocol-owned type or constant).")
        return 0

    for lang in ("go", "ts", "python"):
        if args.monorepo:
            try:
                sources[lang] = load_all(MONOREPO_PATHS[lang], from_github=False)
            except FileNotFoundError as e:
                print(f"check_drift: incomplete monorepo layout: {e}", file=sys.stderr)
                return 2
        elif args.local == lang:
            try:
                sources[lang] = load_all([REPO_ROOT / p for p in LOCAL_PATHS[lang]], from_github=False)
            except FileNotFoundError as e:
                print(f"check_drift: --local={lang} but {e}", file=sys.stderr)
                return 2
        elif args.from_github:
            try:
                sources[lang] = load_from_github(lang, github_refs)
            except RuntimeError as e:
                print(f"check_drift: {e}", file=sys.stderr)
                return 2
        else:
            try:
                sources[lang] = load_all(SIBLING_PATHS[lang], from_github=False)
            except FileNotFoundError:
                print(
                    f"check_drift: sibling working dir for {lang} not found at "
                    f"{', '.join(str(p) for p in SIBLING_PATHS[lang])}; retry with --from-github or --local={lang}",
                    file=sys.stderr,
                )
                return 2

    go_types = parse_go_types(sources["go"])
    ts_types = parse_ts_types(sources["ts"])
    py_types = parse_py_types(sources["python"])

    failures: list[str] = []
    if args.monorepo:
        failures.extend(check_go_sdk(MONOREPO_ROOT / "go", sources["go"]))
    for lang, source, pattern, label in (
        ("go", sources["go"], GO_STRUCT_RE, "struct"),
        ("ts", sources["ts"], TS_INTERFACE_RE, "interface"),
        ("py", sources["python"], PY_CLASS_RE, "TypedDict"),
    ):
        for name in sorted(repeated(m.group(1) for m in pattern.finditer(source))):
            failures.append(
                f"  {name}.{lang}: {label} declared more than once; only the "
                f"last declaration is compared, so the others drift unchecked"
            )

    for name in TRACKED_TYPES:
        go_fields = go_types.get(name)
        if go_fields is None:
            failures.append(f"  {name}: not found in types.go (TRACKED_TYPES out of date?)")
            continue
        ts_fields = ts_types.get(name)
        if ts_fields is None:
            failures.append(f"  {name}: missing TypeScript interface in typescript/src/types.ts")
        else:
            failures.extend(diff_field_sets(name, "ts", go_fields, ts_fields))
        py_fields = py_types.get(name)
        if py_fields is None:
            failures.append(f"  {name}: missing Python TypedDict in python/src/postgrip_agent/types.py")
        else:
            failures.extend(diff_field_sets(name, "py", go_fields, py_fields))

    if failures:
        print("Drift detected:", file=sys.stderr)
        for line in failures:
            print(line, file=sys.stderr)
        print(file=sys.stderr)
        print(
            "Resolve by either updating the missing language to mirror Go "
            "(if the Go change is the source of truth) or rolling back the "
            "Go change. Update tools/check_drift.py:TRACKED_TYPES if a type "
            "was renamed.",
            file=sys.stderr,
        )
        return 1

    print(f"Drift check OK ({len(TRACKED_TYPES)} types verified across go / ts / python).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
