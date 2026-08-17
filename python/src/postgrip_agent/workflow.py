from __future__ import annotations

import asyncio
import ast
import contextvars
import inspect
import textwrap
from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any, Awaitable, Callable, Generic, TypeVar, overload

from .errors import CancelledFailure

F = TypeVar("F", bound=Callable[..., Any])
C = TypeVar("C", bound=type)
R = TypeVar("R")
CancellationScopeType = str


@dataclass(frozen=True)
class WorkflowInfo:
    workflow_id: str
    workflow_run_id: str
    task_queue: str
    workflow_type: str


@dataclass(frozen=True)
class _Definition(Generic[R]):
    name: str
    type: str


SignalDefinition = _Definition[None]
QueryDefinition = _Definition[R]
UpdateDefinition = _Definition[R]


@dataclass
class _WorkflowRuntime:
    workflow_id: str
    workflow_run_id: str
    task_queue: str
    workflow_type: str
    execute_activity: Callable[[str, list[Any], dict[str, Any]], Awaitable[Any]]
    execute_child: Callable[[str, list[Any], dict[str, Any]], Awaitable[Any]]
    continue_as_new: Callable[[str, list[Any], dict[str, Any]], None]
    sleep: Callable[[int, CancellationScopeType], Awaitable[None]]
    condition: Callable[[Callable[[], bool], int | None, CancellationScopeType], Awaitable[bool]]
    cancellation_requested: Callable[[], bool]
    set_handler: Callable[[str, str, Callable[..., Any]], None]
    emit: Callable[[dict[str, Any]], Awaitable[None]]
    scopes: list[CancellationScopeType] = field(default_factory=list)


class ContinueAsNewCommand(Exception):
    def __init__(self, workflow_type: str, args: list[Any], options: dict[str, Any]):
        super().__init__(f"continue workflow as new {workflow_type}")
        self.workflow_type = workflow_type
        self.args = args
        self.options = options


_workflow_runtime: contextvars.ContextVar[_WorkflowRuntime | None] = contextvars.ContextVar("postgrip_workflow_runtime", default=None)
_fallback_scopes: contextvars.ContextVar[list[CancellationScopeType]] = contextvars.ContextVar("postgrip_workflow_scope_stack", default=[])


@overload
def defn(cls: C, /) -> C:
    ...


@overload
def defn(cls: None = None, /, *, name: str | None = None, sandboxed: bool = True) -> Callable[[C], C]:
    ...


def defn(cls: C | None = None, /, *, name: str | None = None, sandboxed: bool = True) -> C | Callable[[C], C]:
    def decorate(target: C) -> C:
        setattr(target, "_postgrip_workflow_name", name or target.__name__)
        setattr(target, "_postgrip_workflow_sandboxed", sandboxed)
        return target

    if cls is not None:
        return decorate(cls)
    return decorate


def run(fn: F) -> F:
    setattr(fn, "_postgrip_workflow_run", True)
    return fn


def workflow_name(workflow: Callable[..., Any] | type | str | None) -> str:
    if workflow is None:
        return ""
    if isinstance(workflow, str):
        return workflow
    return str(getattr(workflow, "_postgrip_workflow_name", getattr(workflow, "__name__", "")))


def workflow_run_method(workflow_cls: type) -> Callable[..., Any]:
    for value in workflow_cls.__dict__.values():
        if callable(value) and getattr(value, "_postgrip_workflow_run", False):
            return value
    candidate = getattr(workflow_cls, "run", None)
    if callable(candidate):
        return candidate
    raise ValueError(f"workflow {workflow_name(workflow_cls)} has no @workflow.run method")


def validate_workflow_sandbox(workflow: Callable[..., Any] | type) -> None:
    if getattr(workflow, "_postgrip_workflow_sandboxed", True) is False:
        return
    target = workflow_run_method(workflow) if inspect.isclass(workflow) else workflow
    try:
        source = inspect.getsource(target)
    except (OSError, TypeError):
        return
    tree = ast.parse(textwrap.dedent(source))
    visitor = _WorkflowSandboxVisitor()
    visitor.visit(tree)
    if visitor.violations:
        joined = "; ".join(visitor.violations)
        raise RuntimeError(f"workflow sandbox rejected nondeterministic API use: {joined}")


def info() -> WorkflowInfo:
    runtime = _current_runtime()
    return WorkflowInfo(
        workflow_id=runtime.workflow_id,
        workflow_run_id=runtime.workflow_run_id,
        task_queue=runtime.task_queue,
        workflow_type=runtime.workflow_type,
    )


def now() -> datetime:
    _current_runtime()
    return datetime.now(timezone.utc)


async def sleep(delay: float | int | timedelta) -> None:
    runtime = _current_runtime()
    duration_ms = _duration_ms(delay)
    _throw_if_scoped_cancelled(runtime)
    await runtime.sleep(duration_ms, _current_scope())


async def condition(predicate: Callable[[], bool], timeout: float | int | timedelta | None = None) -> bool:
    runtime = _current_runtime()
    if predicate():
        return True
    _throw_if_scoped_cancelled(runtime)
    timeout_ms = None if timeout is None else _duration_ms(timeout)
    return await runtime.condition(predicate, timeout_ms, _current_scope())


def cancellation_requested() -> bool:
    runtime = _current_runtime()
    return _current_scope() != "non_cancellable" and runtime.cancellation_requested()


async def milestone(
    name: str,
    *,
    index: int | None = None,
    total: int | None = None,
    status: str = "completed",
    details: dict[str, Any] | None = None,
) -> None:
    runtime = _current_runtime()
    event_details = {
        **(details or {}),
        "milestone": True,
        "name": name,
        "status": status,
    }
    if index is not None:
        event_details["index"] = index
    if total is not None:
        event_details["total"] = total
    await runtime.emit({
        "kind": "milestone",
        "stage": "milestone",
        "message": _milestone_message(name, index=index, total=total, status=status),
        "details": event_details,
    })


async def execute_activity(activity: Callable[..., R] | str, *args: Any, **options: Any) -> R:
    runtime = _current_runtime()
    activity_type = activity if isinstance(activity, str) else str(getattr(activity, "_postgrip_activity_name", activity.__name__))
    _throw_if_scoped_cancelled(runtime)
    return await runtime.execute_activity(activity_type, list(args), {**options, "cancellation_scope": _current_scope()})


def proxy_activities(options: dict[str, Any] | None = None) -> Any:
    class _ActivitiesProxy:
        def __getattr__(self, name: str) -> Callable[..., Awaitable[Any]]:
            async def invoke(*args: Any) -> Any:
                return await execute_activity(name, *args, **(options or {}))

            return invoke

    return _ActivitiesProxy()


async def execute_child(workflow: Callable[..., R] | str, *args: Any, **options: Any) -> R:
    runtime = _current_runtime()
    workflow_type = workflow_name(workflow)
    if not workflow_type:
        raise ValueError("child workflow type is required")
    child_args = list(options.pop("args", list(args)))
    _throw_if_scoped_cancelled(runtime)
    return await runtime.execute_child(workflow_type, child_args, {**options, "cancellation_scope": _current_scope()})


def continue_as_new(workflow: Callable[..., Any] | str | None = None, *args: Any, **options: Any) -> None:
    runtime = _current_runtime()
    workflow_type = options.pop("workflow_type", None) or workflow_name(workflow) or runtime.workflow_type
    next_args = list(options.pop("args", list(args)))
    runtime.continue_as_new(workflow_type, next_args, options)


def define_signal(name: str) -> SignalDefinition:
    return SignalDefinition(name=name, type="signal")


def define_query(name: str) -> QueryDefinition[Any]:
    return QueryDefinition(name=name, type="query")


def define_update(name: str) -> UpdateDefinition[Any]:
    return UpdateDefinition(name=name, type="update")


def set_handler(definition: _Definition[Any] | str, handler: Callable[..., Any]) -> None:
    runtime = _current_runtime()
    if isinstance(definition, str):
        kind = "signal"
        name = definition
    else:
        kind = definition.type
        name = definition.name
    runtime.set_handler(kind, name, handler)


class CancellationScope:
    @staticmethod
    def current() -> str:
        _current_runtime()
        return _current_scope()

    @staticmethod
    async def cancellable(fn: Callable[[], R | Awaitable[R]]) -> R:
        return await _run_in_scope("cancellable", fn)

    @staticmethod
    async def non_cancellable(fn: Callable[[], R | Awaitable[R]]) -> R:
        return await _run_in_scope("non_cancellable", fn)


class _Unsafe:
    @contextmanager
    def imports_passed_through(self):
        yield

    def is_replaying(self) -> bool:
        return False

    def is_replaying_history_events(self) -> bool:
        return False


unsafe = _Unsafe()


async def run_in_workflow_runtime(runtime: _WorkflowRuntime, fn: Callable[[], R | Awaitable[R]]) -> R:
    runtime.scopes = []
    runtime_token = _workflow_runtime.set(runtime)
    try:
        value = fn()
        if inspect.isawaitable(value):
            return await value
        return value
    finally:
        _workflow_runtime.reset(runtime_token)


async def _run_in_scope(scope: CancellationScopeType, fn: Callable[[], R | Awaitable[R]]) -> R:
    scopes = _current_scopes()
    scopes.append(scope)
    try:
        value = fn()
        if inspect.isawaitable(value):
            return await value
        return value
    finally:
        scopes.pop()


def _current_runtime() -> _WorkflowRuntime:
    runtime = _workflow_runtime.get()
    if runtime is None:
        raise RuntimeError("workflow API called outside of a PostGrip workflow runtime")
    return runtime


def _current_scopes() -> list[CancellationScopeType]:
    runtime = _workflow_runtime.get()
    if runtime is not None:
        return runtime.scopes
    return _fallback_scopes.get()


def _current_scope() -> CancellationScopeType:
    scopes = _current_scopes()
    return scopes[-1] if scopes else "cancellable"


def _throw_if_scoped_cancelled(runtime: _WorkflowRuntime) -> None:
    if _current_scope() != "non_cancellable" and runtime.cancellation_requested():
        raise CancelledFailure("workflow cancellation requested")


def _duration_ms(value: float | int | timedelta) -> int:
    if hasattr(value, "total_seconds"):
        return max(0, round(value.total_seconds() * 1000))
    return max(0, round(float(value) * 1000))


def _milestone_message(name: str, *, index: int | None, total: int | None, status: str) -> str:
    prefix = f"step {index}/{total} " if index is not None and total is not None else ""
    return f"{prefix}{status} {name}".strip()


workflow_info = info


class _WorkflowSandboxVisitor(ast.NodeVisitor):
    _banned_attribute_calls = {
        "asyncio.sleep": "use workflow.sleep() for durable timers",
        "random.random": "move randomness into an activity",
        "random.randint": "move randomness into an activity",
        "random.randrange": "move randomness into an activity",
        "random.choice": "move randomness into an activity",
        "random.shuffle": "move randomness into an activity",
        "time.monotonic": "use workflow.now() or an activity",
        "time.perf_counter": "use workflow.now() or an activity",
        "time.sleep": "use workflow.sleep() for durable timers",
        "time.time": "use workflow.now() or an activity",
        "uuid.uuid4": "generate IDs in the client, options, or an activity",
        "datetime.datetime.now": "use workflow.now()",
        "datetime.datetime.utcnow": "use workflow.now()",
    }
    _banned_direct_calls = {
        "choice": "move randomness into an activity",
        "randint": "move randomness into an activity",
        "random": "move randomness into an activity",
        "randrange": "move randomness into an activity",
        "shuffle": "move randomness into an activity",
        "sleep": "use workflow.sleep() for durable timers",
        "time": "use workflow.now() or an activity",
        "uuid4": "generate IDs in the client, options, or an activity",
    }

    def __init__(self) -> None:
        self.violations: list[str] = []

    def visit_Call(self, node: ast.Call) -> Any:
        dotted = self._dotted_name(node.func)
        if dotted in self._banned_attribute_calls:
            self.violations.append(f"{dotted}() is not allowed, {self._banned_attribute_calls[dotted]}")
        if isinstance(node.func, ast.Name) and node.func.id in self._banned_direct_calls:
            self.violations.append(f"{node.func.id}() is not allowed, {self._banned_direct_calls[node.func.id]}")
        self.generic_visit(node)

    def _dotted_name(self, node: ast.AST) -> str:
        if isinstance(node, ast.Name):
            return node.id
        if isinstance(node, ast.Attribute):
            prefix = self._dotted_name(node.value)
            return f"{prefix}.{node.attr}" if prefix else node.attr
        return ""


__all__ = [
    "CancellationScope",
    "ContinueAsNewCommand",
    "QueryDefinition",
    "SignalDefinition",
    "UpdateDefinition",
    "WorkflowInfo",
    "cancellation_requested",
    "condition",
    "continue_as_new",
    "define_query",
    "define_signal",
    "define_update",
    "defn",
    "execute_activity",
    "execute_child",
    "info",
    "milestone",
    "now",
    "proxy_activities",
    "run",
    "run_in_workflow_runtime",
    "set_handler",
    "sleep",
    "unsafe",
    "validate_workflow_sandbox",
    "workflow_info",
    "workflow_name",
    "workflow_run_method",
]
