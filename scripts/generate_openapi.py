#!/usr/bin/env python3
"""Generate SDK operation metadata and wire types from openapi.json."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "openapi.json"
HTTP_METHODS = {"delete", "get", "head", "options", "patch", "post", "put", "trace"}
GO_PROTOCOL_SCHEMAS = {
    "TaskState", "TaskEventKind", "WorkflowState", "WorkflowIdReusePolicy",
    "ScheduleState", "ScheduleOverlapPolicy", "ScheduleMissedRunPolicy",
    "SandboxBackend", "SandboxDesiredState", "SandboxObservedState",
    "Namespace", "CreateNamespaceRequest", "CompactRequest", "CompactResponse",
    "EnqueueTaskRequest", "Task", "TaskResult", "FailureInfo", "ContinueAsNewResult",
    "TaskEventInput", "TaskEvent", "RetryPolicy", "ScheduleCalendarSpec", "ScheduleSpec",
    "ScheduleAction", "Schedule", "CreateScheduleRequest", "UpdateScheduleRequest",
    "PauseScheduleRequest", "UnpauseScheduleRequest", "TriggerScheduleRequest",
    "TriggerScheduleResponse", "BackfillScheduleRequest", "BackfillScheduleResponse",
    "WorkflowExecution", "WorkflowHistoryEvent", "WorkflowCountResponse",
    "SignalWorkflowRequest", "SignalWithStartWorkflowRequest", "SignalWithStartWorkflowResponse",
    "CancelWorkflowRequest", "TerminateWorkflowRequest", "AgentPollDirective", "PollTaskResponse",
    "HeartbeatTaskRequest", "AppendTaskEventRequest", "CompleteTaskRequest", "BlockTaskRequest",
    "FailTaskRequest", "RefreshAgentSessionRequest", "AgentSessionResponse",
    "SandboxResourceLimits", "SandboxPortMapping", "SandboxNetworkPolicy", "Sandbox",
    "SandboxCreateRequest", "SandboxListResponse", "CreateSandboxSessionRequest",
    "CreateSandboxSessionResponse", "SandboxWorkspace",
}
GO_PROTOCOL_TYPE_OVERRIDES = {"WorkflowIdReusePolicy": "WorkflowIDReusePolicy"}
GO_COMPATIBILITY_EXCLUDED_SCHEMAS = {"ErrorResponse"}


def load_contract() -> tuple[dict[str, Any], str]:
    raw = SPEC_PATH.read_bytes()
    spec = json.loads(raw)
    if not str(spec.get("openapi", "")).startswith("3.1."):
        raise ValueError("openapi.json must use OpenAPI 3.1")
    return spec, hashlib.sha256(raw).hexdigest()


def resolve_ref(spec: dict[str, Any], value: dict[str, Any]) -> dict[str, Any]:
    """Resolve a local OpenAPI reference used outside a schema."""
    while "$ref" in value:
        reference = value["$ref"]
        if not reference.startswith("#/"):
            raise ValueError(f"only local OpenAPI references are supported: {reference}")
        resolved: Any = spec
        for part in reference[2:].split("/"):
            resolved = resolved[part.replace("~1", "/").replace("~0", "~")]
        if not isinstance(resolved, dict):
            raise ValueError(f"OpenAPI reference does not resolve to an object: {reference}")
        value = resolved
    return value


def content_schema(spec: dict[str, Any], value: dict[str, Any]) -> dict[str, Any] | None:
    value = resolve_ref(spec, value)
    content = value.get("content", {})
    if "application/json" in content:
        media_type = "application/json"
    elif "application/gzip" in content:
        media_type = "application/gzip"
    elif content:
        raise ValueError(f"unsupported OpenAPI content types: {sorted(content)}")
    else:
        return None
    schema = content[media_type].get("schema")
    if not isinstance(schema, dict):
        raise ValueError(f"OpenAPI {media_type} content is missing its schema")
    return schema


def successful_response_schema(spec: dict[str, Any], operation: dict[str, Any]) -> dict[str, Any] | None:
    responses = operation.get("responses", {})
    successful = sorted(
        (code, response)
        for code, response in responses.items()
        if str(code).isdigit() and 200 <= int(code) < 300
    )
    if not successful:
        return None
    return content_schema(spec, successful[0][1])


def operation_parameters(
    spec: dict[str, Any], path_item: dict[str, Any], operation: dict[str, Any], location: str
) -> dict[str, dict[str, Any]]:
    parameters: dict[str, dict[str, Any]] = {}
    for raw_parameter in [*path_item.get("parameters", []), *operation.get("parameters", [])]:
        parameter = resolve_ref(spec, raw_parameter)
        if parameter.get("in") == location:
            parameters[parameter["name"]] = parameter
    return parameters


def validate_schema_references(spec: dict[str, Any]) -> None:
    schemas = spec.get("components", {}).get("schemas", {})

    def visit(value: Any) -> None:
        if isinstance(value, dict):
            if value.get("type") == "object" or "properties" in value or "additionalProperties" in value:
                if "additionalProperties" not in value:
                    raise ValueError("object schema must declare additionalProperties explicitly")
                if value["additionalProperties"] is True:
                    raise ValueError("untyped additionalProperties are not allowed; declare a value schema")
            reference = value.get("$ref")
            if reference and reference.startswith("#/components/schemas/"):
                name = reference.rsplit("/", 1)[-1]
                if name not in schemas:
                    raise ValueError(f"schema reference does not exist: {reference}")
            for child in value.values():
                visit(child)
        elif isinstance(value, list):
            for child in value:
                visit(child)

    visit(spec)


def operation_rows(spec: dict[str, Any]) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    seen: set[str] = set()
    for route_path, path_item in spec.get("paths", {}).items():
        for method, operation in path_item.items():
            if method not in HTTP_METHODS or not operation.get("x-postgrip-sdk-generate"):
                continue
            operation_id = operation.get("operationId")
            if not operation_id or operation_id in seen:
                raise ValueError(f"missing or duplicate operationId: {operation_id!r}")
            seen.add(operation_id)
            request_body = operation.get("requestBody")
            path_parameters = operation_parameters(spec, path_item, operation, "path")
            query_parameters = operation_parameters(spec, path_item, operation, "query")
            header_parameters = operation_parameters(spec, path_item, operation, "header")
            placeholders = re.findall(r"\{([^}]+)\}", route_path)
            if set(placeholders) != set(path_parameters):
                raise ValueError(
                    f"operation {operation_id} path placeholders {placeholders} do not match "
                    f"declared parameters {sorted(path_parameters)}"
                )
            rows.append(
                {
                    "id": operation_id,
                    "method": method.upper(),
                    "path": route_path,
                    "auth": operation.get("x-postgrip-auth-lane", ""),
                    "signing": operation.get("x-postgrip-request-signing", ""),
                    "streaming": bool(operation.get("x-postgrip-streaming-request")),
                    "websocket": bool(operation.get("x-postgrip-websocket")),
                    "parameters": placeholders,
                    "path_parameter_schemas": path_parameters,
                    "query_parameter_schemas": query_parameters,
                    "header_parameter_schemas": header_parameters,
                    "request_schema": content_schema(spec, request_body) if request_body else None,
                    "request_required": bool(request_body and resolve_ref(spec, request_body).get("required")),
                    "response_schema": successful_response_schema(spec, operation),
                }
            )
    if not rows:
        raise ValueError("openapi.json contains no generated operations")
    return sorted(rows, key=lambda row: row["id"])


def go_name(operation_id: str) -> str:
    return operation_id[:1].upper() + operation_id[1:]


def snake_name(operation_id: str) -> str:
    return re.sub(r"(?<!^)(?=[A-Z])", "_", operation_id).upper()


def pascal_name(value: str) -> str:
    words = re.sub(r"([a-z0-9])([A-Z])", r"\1 \2", value)
    words = re.sub(r"[^A-Za-z0-9]+", " ", words)
    return "".join(word[:1].upper() + word[1:] for word in words.split())


def constant_name(value: str) -> str:
    words = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", value)
    return re.sub(r"[^A-Za-z0-9]+", "_", words).strip("_").upper()


def schema_ref_name(schema: dict[str, Any]) -> str | None:
    reference = schema.get("$ref")
    prefix = "#/components/schemas/"
    return reference[len(prefix):] if isinstance(reference, str) and reference.startswith(prefix) else None


def schema_label(schema: dict[str, Any] | None) -> str:
    if not schema:
        return ""
    return schema_ref_name(schema) or "inline"


def go_schema_type(schema: dict[str, Any], optional: bool = False) -> str:
    reference_name = schema_ref_name(schema)
    if reference_name:
        if reference_name == "JsonValue":
            return "any"
        value = f"OpenAPI{pascal_name(reference_name)}"
        return f"*{value}" if optional else value
    if not schema:
        return "any"
    variants = schema.get("oneOf") or schema.get("anyOf")
    if variants:
        non_null = [variant for variant in variants if variant.get("type") != "null"]
        if len(non_null) == 1 and len(non_null) != len(variants):
            value = go_schema_type(non_null[0])
            if value == "any" or value.startswith(("*", "[]", "map[")):
                return value
            return f"*{value}"
        return "any"
    schema_type = schema.get("type")
    if schema_type == "array":
        return f"[]{go_schema_type(schema.get('items', {}))}"
    if schema_type == "object" or "properties" in schema or "additionalProperties" in schema:
        additional = schema.get("additionalProperties")
        if isinstance(additional, dict):
            return f"map[string]{go_schema_type(additional)}"
        if schema.get("properties"):
            fields = go_struct_fields(schema)
            return f"struct {{\n{fields}\n}}"
        return "map[string]any" if additional is not False else "struct{}"
    if schema_type == "boolean":
        value = "bool"
    elif schema_type == "integer":
        value = "int64"
    elif schema_type == "number":
        value = "float64"
    elif schema_type == "string" and schema.get("format") == "date-time":
        value = "time.Time"
    elif schema_type == "string" and schema.get("format") == "binary":
        return "[]byte"
    else:
        value = "string" if schema_type == "string" else "any"
    return f"*{value}" if optional and value != "any" else value


def go_struct_fields(schema: dict[str, Any]) -> str:
    required = set(schema.get("required", []))
    fields: list[str] = []
    for name, property_schema in schema.get("properties", {}).items():
        optional = name not in required
        field_type = go_schema_type(property_schema, optional=optional)
        tag = name + (",omitempty" if optional else "")
        fields.append(f"\t{pascal_name(name)} {field_type} `json:{json.dumps(tag)}`")
    return "\n".join(fields)


def go_types_source(spec: dict[str, Any], rows: list[dict[str, Any]], digest: str) -> str:
    declarations: list[str] = []
    compatibility_aliases: list[str] = []
    enum_constants: list[str] = []
    for name, schema in spec.get("components", {}).get("schemas", {}).items():
        type_name = f"OpenAPI{pascal_name(name)}"
        if name in GO_PROTOCOL_SCHEMAS:
            protocol_name = GO_PROTOCOL_TYPE_OVERRIDES.get(name, name)
            declarations.append(f"type {type_name} = protocol.{protocol_name}")
            compatibility_alias = f"type {pascal_name(name)} = protocol.{protocol_name}"
        elif schema.get("type") == "object" or "properties" in schema:
            declarations.append(f"type {type_name} struct {{\n{go_struct_fields(schema)}\n}}")
            compatibility_alias = f"type {pascal_name(name)} = {type_name}"
        elif not schema:
            declarations.append(f"type {type_name} = any")
            compatibility_alias = f"type {pascal_name(name)} = {type_name}"
        else:
            declarations.append(f"type {type_name} {go_schema_type(schema)}")
            compatibility_alias = f"type {pascal_name(name)} = {type_name}"
        if name not in GO_COMPATIBILITY_EXCLUDED_SCHEMAS:
            compatibility_aliases.append(compatibility_alias)
        if schema.get("enum"):
            enum_constants.extend(
                f"\tOpenAPI{pascal_name(name)}{pascal_name(str(value))} {type_name} = {json.dumps(value)}"
                for value in schema["enum"]
            )
    for row in rows:
        operation_name = pascal_name(row["id"])
        request_type = go_schema_type(row["request_schema"] or {}) if row["request_schema"] else "OpenAPINoBody"
        response_type = go_schema_type(row["response_schema"] or {}) if row["response_schema"] else "OpenAPINoBody"
        declarations.append(f"type OpenAPI{operation_name}RequestBody = {request_type}")
        declarations.append(f"type OpenAPI{operation_name}ResponseBody = {response_type}")
    declarations_source = chr(10).join(declarations)
    imports = ['protocol "github.com/postgrip-io/agent-sdk-protocol"']
    if "time.Time" in declarations_source:
        imports.append('"time"')
    import_source = "\n".join(f"\t{value}" for value in imports)
    source = f'''// Code generated by scripts/generate_openapi.py; DO NOT EDIT.

package client

import (
{import_source}
)

// These public models provide a complete, generated low-level wire surface.
// Existing protocol aliases remain compatibility adapters for the ergonomic SDK.
type OpenAPINoBody struct{{}}

{declarations_source}

// OpenAPI-prefixed constants expose every generated enum value without
// colliding with the ergonomic SDK's compatibility constants.
const (
{chr(10).join(enum_constants)}
)

// Compatibility names preserve the existing SDK surface while making the
// generated OpenAPI models its implementation.
{chr(10).join(compatibility_aliases)}

const openAPITypesSpecSHA256 = "{digest}"
'''
    try:
        formatted = subprocess.run(
            ["gofmt"], input=source, text=True, capture_output=True, check=True
        )
    except (FileNotFoundError, subprocess.CalledProcessError) as exc:
        detail = getattr(exc, "stderr", "")
        raise RuntimeError(f"gofmt is required to generate OpenAPI types: {detail}") from exc
    return formatted.stdout


def go_query_struct(row: dict[str, Any]) -> tuple[str, str]:
    parameters = row["query_parameter_schemas"]
    if not parameters:
        return "", "nil"
    type_name = f"OpenAPI{pascal_name(row['id'])}Query"
    fields: list[str] = []
    encoders: list[str] = []
    for name, parameter in parameters.items():
        schema = parameter.get("schema", {})
        required = bool(parameter.get("required"))
        field_name = pascal_name(name.replace(".*", ""))
        if name.endswith(".*"):
            field_type = "map[string]any"
            encoders.append(
                f'\tfor key, value := range p.{field_name} {{ values.Set({json.dumps(name[:-1])}+key, fmt.Sprint(value)) }}'
            )
        else:
            field_type = go_schema_type(schema, optional=not required)
            if required:
                encoders.append(f'\tvalues.Set({json.dumps(name)}, fmt.Sprint(p.{field_name}))')
            elif field_type.startswith("[]"):
                encoders.append(
                    f'\tfor _, value := range p.{field_name} {{ values.Add({json.dumps(name)}, fmt.Sprint(value)) }}'
                )
            elif field_type == "any":
                encoders.append(
                    f'\tif p.{field_name} != nil {{ values.Set({json.dumps(name)}, fmt.Sprint(p.{field_name})) }}'
                )
            else:
                encoders.append(
                    f'\tif p.{field_name} != nil {{ values.Set({json.dumps(name)}, fmt.Sprint(*p.{field_name})) }}'
                )
        fields.append(f"\t{field_name} {field_type}")
    declaration = f'''type {type_name} struct {{
{chr(10).join(fields)}
}}

func (p {type_name}) values() url.Values {{
\tvalues := url.Values{{}}
{chr(10).join(encoders)}
\treturn values
}}'''
    return declaration, "query.values()"


def go_openapi_client_source(rows: list[dict[str, Any]], digest: str) -> str:
    query_types: list[str] = []
    methods: list[str] = []
    for row in rows:
        if row["streaming"] or row["websocket"]:
            continue
        operation_name = pascal_name(row["id"])
        parameters = ["ctx context.Context"]
        path_values: list[str] = []
        for name in row["parameters"]:
            argument = name[:1].lower() + pascal_name(name)[1:]
            parameters.append(f"{argument} string")
            path_values.append(f"{json.dumps(name)}: {argument}")
        query_declaration, query_expression = go_query_struct(row)
        if query_declaration:
            query_types.append(query_declaration)
            parameters.append(f"query OpenAPI{operation_name}Query")
        if row["request_schema"]:
            body_type = f"OpenAPI{operation_name}RequestBody"
            if row["request_required"]:
                parameters.append(f"body {body_type}")
                body_expression = "body"
            else:
                parameters.append(f"body *{body_type}")
                body_expression = "body"
        else:
            body_expression = "nil"
        path_expression = f"map[string]string{{{', '.join(path_values)}}}" if path_values else "nil"
        call = (
            f"c.connection.doOpenAPI(ctx, openAPI{operation_name}, {path_expression}, "
            f"{query_expression}, {body_expression}, "
        )
        response_schema = row["response_schema"]
        if response_schema:
            response_type = f"OpenAPI{operation_name}ResponseBody"
            methods.append(
                f'''func (c *OpenAPIClient) {operation_name}({", ".join(parameters)}) ({response_type}, error) {{
\tvar response {response_type}
\terr := {call}&response)
\treturn response, err
}}'''
            )
        else:
            methods.append(
                f'''func (c *OpenAPIClient) {operation_name}({", ".join(parameters)}) error {{
\treturn {call}nil)
}}'''
            )
    source = f'''// Code generated by scripts/generate_openapi.py; DO NOT EDIT.

package client

import (
\t"context"
\t"fmt"
\t"net/url"
)

// OpenAPIClient is the generated low-level JSON API. Streaming workspace
// uploads and WebSocket sessions remain on Connection's custom adapters.
type OpenAPIClient struct {{
\tconnection *Connection
}}

// OpenAPI returns the generated low-level client bound to this connection.
func (c *Connection) OpenAPI() *OpenAPIClient {{
\treturn &OpenAPIClient{{connection: c}}
}}

{chr(10).join(query_types)}

{chr(10).join(methods)}

// OpenAPIClientOperationCount excludes the streaming upload and WebSocket
// connection operations, which use Connection's custom adapters.
const OpenAPIClientOperationCount = {sum(not row['streaming'] and not row['websocket'] for row in rows)}

const openAPIClientSpecSHA256 = "{digest}"
'''
    try:
        formatted = subprocess.run(
            ["gofmt"], input=source, text=True, capture_output=True, check=True
        )
    except (FileNotFoundError, subprocess.CalledProcessError) as exc:
        detail = getattr(exc, "stderr", "")
        raise RuntimeError(f"gofmt is required to generate the OpenAPI client: {detail}") from exc
    return formatted.stdout


def typescript_schema_type(schema: dict[str, Any], indent: str = "") -> str:
    reference_name = schema_ref_name(schema)
    if reference_name:
        return f"OpenAPIComponents[{json.dumps(reference_name)}]"
    if not schema:
        return "unknown"
    if schema.get("enum"):
        return " | ".join(json.dumps(value) for value in schema["enum"])
    if schema.get("oneOf") or schema.get("anyOf"):
        variants = schema.get("oneOf") or schema.get("anyOf")
        return " | ".join(typescript_schema_type(value, indent) for value in variants)
    if schema.get("allOf"):
        return " & ".join(typescript_schema_type(value, indent) for value in schema["allOf"])
    schema_type = schema.get("type")
    if schema_type == "array":
        return f"Array<{typescript_schema_type(schema.get('items', {}), indent)}>"
    if schema_type == "object" or "properties" in schema or "additionalProperties" in schema:
        properties = schema.get("properties", {})
        additional = schema.get("additionalProperties")
        if not properties:
            if isinstance(additional, dict):
                return f"Record<string, {typescript_schema_type(additional, indent)}>"
            return "Record<string, unknown>" if additional is not False else "Record<string, never>"
        required = set(schema.get("required", []))
        child_indent = indent + "  "
        fields = [
            f"{child_indent}{json.dumps(name)}{'?' if name not in required else ''}: "
            f"{typescript_schema_type(value, child_indent)};"
            for name, value in properties.items()
        ]
        value = "{\n" + "\n".join(fields) + f"\n{indent}}}"
        if isinstance(additional, dict):
            value += f" & Record<string, {typescript_schema_type(additional, indent)}>"
        return value
    if schema_type in {"integer", "number"}:
        return "number"
    if schema_type == "boolean":
        return "boolean"
    if schema_type == "string" and schema.get("format") == "binary":
        return "Uint8Array"
    if schema_type == "string":
        return "string"
    if schema_type == "null":
        return "null"
    return "unknown"


def typescript_parameter_type(parameters: dict[str, dict[str, Any]], indent: str = "") -> str:
    if not parameters:
        return "undefined"
    fields = []
    child_indent = indent + "  "
    for name, parameter in parameters.items():
        optional = "" if parameter.get("required") else "?"
        fields.append(
            f"{child_indent}readonly {json.dumps(name)}{optional}: "
            f"{typescript_schema_type(parameter.get('schema', {}), child_indent)};"
        )
    return "{\n" + "\n".join(fields) + f"\n{indent}}}"


def python_schema_type(schema: dict[str, Any]) -> str:
    reference_name = schema_ref_name(schema)
    if reference_name:
        return f"_Schema{pascal_name(reference_name)}"
    if not schema:
        return "Any"
    if schema.get("enum"):
        values = ", ".join(repr(value) for value in schema["enum"])
        return f"Literal[{values}]"
    variants = schema.get("oneOf") or schema.get("anyOf")
    if variants:
        return " | ".join(python_schema_type(value) for value in variants)
    schema_type = schema.get("type")
    if schema_type == "array":
        return f"list[{python_schema_type(schema.get('items', {}))}]"
    if schema_type == "object" or "properties" in schema or "additionalProperties" in schema:
        additional = schema.get("additionalProperties")
        if isinstance(additional, dict):
            return f"dict[str, {python_schema_type(additional)}]"
        return "dict[str, Any]"
    if schema_type == "boolean":
        return "bool"
    if schema_type == "integer":
        return "int"
    if schema_type == "number":
        return "float"
    if schema_type == "string" and schema.get("format") == "binary":
        return "bytes"
    if schema_type == "string":
        return "str"
    if schema_type == "null":
        return "None"
    return "Any"


def python_types_source(spec: dict[str, Any], rows: list[dict[str, Any]]) -> str:
    declarations: list[str] = []
    public_aliases: list[str] = []
    for name, schema in spec.get("components", {}).get("schemas", {}).items():
        type_name = f"_Schema{pascal_name(name)}"
        if schema.get("type") == "object" or "properties" in schema:
            required = set(schema.get("required", []))
            required_fields = []
            optional_fields = []
            for property_name, property_schema in schema.get("properties", {}).items():
                annotation = python_schema_type(property_schema)
                target = required_fields if property_name in required else optional_fields
                target.append(f"    {property_name}: {annotation}")
            if optional_fields:
                optional_name = f"{type_name}Optional"
                declarations.append(
                    f"class {optional_name}(TypedDict, total=False):\n" + "\n".join(optional_fields)
                )
                body = "\n".join(required_fields) if required_fields else "    pass"
                declarations.append(f"class {type_name}({optional_name}):\n{body}")
            else:
                body = "\n".join(required_fields) if required_fields else "    pass"
                declarations.append(f"class {type_name}(TypedDict):\n{body}")
        else:
            declarations.append(f"{type_name}: TypeAlias = {python_schema_type(schema)}")
        public_aliases.append(f"OpenAPI{pascal_name(name)}: TypeAlias = {type_name}")

    operation_types: list[str] = []
    operation_map_rows: list[str] = []
    for row in rows:
        name = pascal_name(row["id"])
        request_type = python_schema_type(row["request_schema"] or {}) if row["request_schema"] else "None"
        response_type = python_schema_type(row["response_schema"] or {}) if row["response_schema"] else "None"
        operation_types.append(f"{name}RequestBody: TypeAlias = {request_type}")
        operation_types.append(f"{name}ResponseBody: TypeAlias = {response_type}")
        operation_map_rows.append(
            f"    OperationId.{snake_name(row['id'])}: ({name}RequestBody, {name}ResponseBody),"
        )
    return "\n\n".join(declarations) + "\n\n" + "\n".join(public_aliases) + "\n\n" + "\n".join(operation_types) + "\n\n" + (
        "OPENAPI_OPERATION_TYPES: dict[OperationId, tuple[object, object]] = {\n"
        + "\n".join(operation_map_rows)
        + "\n}\n"
    )


def python_name(value: str) -> str:
    return re.sub(r"(?<!^)(?=[A-Z])", "_", value).lower()


def python_openapi_client_source(rows: list[dict[str, Any]]) -> str:
    methods: list[str] = []
    for row in rows:
        if row["streaming"] or row["websocket"]:
            continue
        arguments = ["self"]
        path_items: list[str] = []
        for name, parameter in row["path_parameter_schemas"].items():
            argument = python_name(name)
            annotation = python_schema_type(parameter.get("schema", {}))
            arguments.append(f"{argument}: {annotation}")
            path_items.append(f"{name!r}: {argument}")
        if row["request_schema"]:
            body_type = f"{pascal_name(row['id'])}RequestBody"
            default = "" if row["request_required"] else " | None = None"
            arguments.append(f"body: {body_type}{default}")
        keyword_arguments: list[str] = []
        query_lines = ["        _query: dict[str, Any] = {}"]
        for name, parameter in row["query_parameter_schemas"].items():
            argument = "search" if name.endswith(".*") else python_name(name)
            required = bool(parameter.get("required"))
            if name.endswith(".*"):
                annotation = "Mapping[str, Any]"
            else:
                annotation = python_schema_type(parameter.get("schema", {}))
            keyword_arguments.append(f"{argument}: {annotation}" + ("" if required else " | None = None"))
            if name.endswith(".*"):
                query_lines.append(
                    f"        if {argument}: _query.update({{f'{name[:-1]}{{key}}': value for key, value in {argument}.items()}})"
                )
            elif required:
                query_lines.append(f"        _query[{name!r}] = {argument}")
            else:
                query_lines.append(f"        if {argument} is not None: _query[{name!r}] = {argument}")
        if keyword_arguments:
            arguments.append("*")
            arguments.extend(keyword_arguments)
        call_arguments = [f"OperationId.{snake_name(row['id'])}"]
        if row["request_schema"]:
            call_arguments.append("body")
        call_keywords: list[str] = []
        if path_items:
            call_keywords.append("path_parameters={" + ", ".join(path_items) + "}")
        if row["query_parameter_schemas"]:
            call_keywords.append("query=_query")
        call_arguments.extend(call_keywords)
        response_type = f"{pascal_name(row['id'])}ResponseBody"
        body_lines = query_lines if row["query_parameter_schemas"] else []
        body_lines.append(
            f"        return cast({response_type}, self._transport({', '.join(call_arguments)}))"
        )
        methods.append(
            f"    def {python_name(row['id'])}({', '.join(arguments)}) -> {response_type}:\n"
            + "\n".join(body_lines)
        )
    return '''class OpenAPITransport(Protocol):
    def __call__(
        self,
        operation_id: OperationId,
        body: Any = None,
        *,
        path_parameters: dict[str, str] | None = None,
        query: dict[str, Any] | None = None,
    ) -> Any: ...


class OpenAPIClient:
    """Generated low-level JSON client; streaming and WebSockets remain custom."""

    def __init__(self, transport: OpenAPITransport):
        self._transport = transport

''' + "\n\n".join(methods) + "\n"


def go_source(rows: list[dict[str, Any]], digest: str) -> str:
    constants = "\n".join(
        f'\topenAPI{go_name(row["id"])} openAPIOperationID = {json.dumps(row["id"])}'
        for row in rows
    )
    custom_parameter_constants = "\n".join(
        f'\tOpenAPI{pascal_name(row["id"])}{pascal_name(location)}{pascal_name(name)} = {json.dumps(name)}'
        for row in rows
        if row["streaming"] or row["websocket"]
        for location, parameters in (
            ("path", row["path_parameter_schemas"]),
            ("query", row["query_parameter_schemas"]),
            ("header", row["header_parameter_schemas"]),
        )
        for name in parameters
    )
    table_rows = []
    for row in rows:
        parameters = ", ".join(json.dumps(value) for value in row["parameters"])
        table_rows.append(
            f'\topenAPI{go_name(row["id"])}: {{Method: {json.dumps(row["method"])}, '
            f'Path: {json.dumps(row["path"])}, AuthLane: {json.dumps(row["auth"])}, '
            f'Signing: {json.dumps(row["signing"])}, StreamingRequest: {str(row["streaming"]).lower()}, '
            f'WebSocket: {str(row["websocket"]).lower()}, RequestSchema: {json.dumps(schema_label(row["request_schema"]))}, '
            f'ResponseSchema: {json.dumps(schema_label(row["response_schema"]))}, PathParameters: []string{{{parameters}}}}},'
        )
    source = f'''// Code generated by scripts/generate_openapi.py; DO NOT EDIT.

package client

import (
\t"fmt"
\t"net/url"
\t"strings"
)

const (
	OpenAPISpecSHA256 = "{digest}"
	OpenAPIOperationCount = {len(rows)}
)

type openAPIOperationID string

const (
{constants}
)

// Parameter names are generated for streaming and WebSocket custom transports.
const (
{custom_parameter_constants}
)

type openAPIOperation struct {{
\tMethod           string
\tPath             string
\tAuthLane         string
\tSigning          string
\tStreamingRequest bool
\tWebSocket        bool
\tRequestSchema    string
\tResponseSchema   string
\tPathParameters   []string
}}

var openAPIOperationTable = map[openAPIOperationID]openAPIOperation{{
{chr(10).join(table_rows)}
}}

func resolveOpenAPIOperation(id openAPIOperationID, pathParameters map[string]string, query url.Values) (openAPIOperation, error) {{
\toperation, ok := openAPIOperationTable[id]
\tif !ok {{
\t\treturn openAPIOperation{{}}, fmt.Errorf("postgrip-agent: unknown OpenAPI operation %q", id)
\t}}
\tfor _, name := range operation.PathParameters {{
\t\tvalue, exists := pathParameters[name]
\t\tif !exists {{
\t\t\treturn openAPIOperation{{}}, fmt.Errorf("postgrip-agent: OpenAPI operation %q requires path parameter %q", id, name)
\t\t}}
\t\toperation.Path = strings.ReplaceAll(operation.Path, "{{"+name+"}}", url.PathEscape(value))
\t}}
\tif encoded := query.Encode(); encoded != "" {{
\t\toperation.Path += "?" + encoded
\t}}
\treturn operation, nil
}}
'''
    try:
        formatted = subprocess.run(
            ["gofmt"], input=source, text=True, capture_output=True, check=True
        )
    except (FileNotFoundError, subprocess.CalledProcessError) as exc:
        raise RuntimeError("gofmt is required to generate OpenAPI bindings") from exc
    return formatted.stdout


def typescript_source(spec: dict[str, Any], rows: list[dict[str, Any]], digest: str) -> str:
    entries = []
    for row in rows:
        entry = {
            "method": row["method"],
            "path": row["path"],
            "authLane": row["auth"],
            "signing": row["signing"] or None,
            "streamingRequest": row["streaming"],
            "webSocket": row["websocket"],
            "requestSchema": schema_label(row["request_schema"]) or None,
            "responseSchema": schema_label(row["response_schema"]) or None,
            "pathParameters": row["parameters"],
        }
        entries.append(f'  {row["id"]}: {json.dumps(entry, separators=(",", ":"))},')
    component_entries = [
        f"  readonly {json.dumps(name)}: {typescript_schema_type(schema, '  ')};"
        for name, schema in spec.get("components", {}).get("schemas", {}).items()
    ]
    component_aliases = [
        f"export type OpenAPI{pascal_name(name)} = OpenAPIComponents[{json.dumps(name)}];"
        for name in spec.get("components", {}).get("schemas", {})
    ]
    custom_parameter_constants = [
        f"export const {constant_name(row['id'])}_{constant_name(location)}_{constant_name(name)} = {json.dumps(name)} as const;"
        for row in rows
        if row["streaming"] or row["websocket"]
        for location, parameters in (
            ("path", row["path_parameter_schemas"]),
            ("query", row["query_parameter_schemas"]),
            ("header", row["header_parameter_schemas"]),
        )
        for name in parameters
    ]
    operation_type_entries = []
    client_methods = []
    operation_aliases = []
    for row in rows:
        request_type = typescript_schema_type(row["request_schema"]) if row["request_schema"] else "undefined"
        if row["request_schema"] and not row["request_required"]:
            request_type += " | undefined"
        response_type = typescript_schema_type(row["response_schema"]) if row["response_schema"] else "undefined"
        path_type = typescript_parameter_type(row["path_parameter_schemas"], "    ")
        query_type = typescript_parameter_type(row["query_parameter_schemas"], "    ")
        header_type = typescript_parameter_type(row["header_parameter_schemas"], "    ")
        operation_type_entries.append(
            f"  readonly {row['id']}: {{\n"
            f"    readonly requestBody: {request_type};\n"
            f"    readonly responseBody: {response_type};\n"
            f"    readonly pathParameters: {path_type};\n"
            f"    readonly queryParameters: {query_type};\n"
            f"    readonly headerParameters: {header_type};\n"
            f"    readonly queryRequired: {str(any(parameter.get('required') for parameter in row['query_parameter_schemas'].values())).lower()};\n"
            "  };"
        )
        operation_aliases.append(
            f"export type {pascal_name(row['id'])}RequestBody = OpenAPIRequestBody<{json.dumps(row['id'])}>;"
        )
        operation_aliases.append(
            f"export type {pascal_name(row['id'])}ResponseBody = OpenAPIResponseBody<{json.dumps(row['id'])}>;"
        )
        if not row["streaming"] and not row["websocket"]:
            options_required = bool(
                row["parameters"]
                or row["request_schema"] and row["request_required"]
                or any(parameter.get("required") for parameter in row["query_parameter_schemas"].values())
                or any(parameter.get("required") for parameter in row["header_parameter_schemas"].values())
            )
            default = "" if options_required else f" = {{}} as OpenAPIRequestOptions<{json.dumps(row['id'])}>"
            client_methods.append(
                f"  {row['id']}(options: OpenAPIRequestOptions<{json.dumps(row['id'])}>{default}): "
                f"Promise<OpenAPIResponseBody<{json.dumps(row['id'])}>> {{\n"
                f"    return this.transport({json.dumps(row['id'])}, options);\n"
                "  }"
            )
    return f'''// Code generated by scripts/generate_openapi.py; DO NOT EDIT.

export const OPENAPI_SPEC_SHA256 = '{digest}';
export const OPENAPI_OPERATION_COUNT = {len(rows)};
export const OPENAPI_CLIENT_OPERATION_COUNT = {sum(not row['streaming'] and not row['websocket'] for row in rows)};

{chr(10).join(custom_parameter_constants)}

export interface OpenAPIComponents {{
{chr(10).join(component_entries)}
}}

{chr(10).join(component_aliases)}

export interface OpenAPIOperationTypes {{
{chr(10).join(operation_type_entries)}
}}

const operationTable = {{
{chr(10).join(entries)}
}} as const;

export type OpenAPIOperationId = keyof typeof operationTable;
export type OpenAPIAuthLane = 'agent' | 'either' | 'global-admin' | 'management' | 'public';
export type OpenAPIRequestBody<I extends OpenAPIOperationId> = OpenAPIOperationTypes[I]['requestBody'];
export type OpenAPIResponseBody<I extends OpenAPIOperationId> = OpenAPIOperationTypes[I]['responseBody'];
export type OpenAPIPathParameters<I extends OpenAPIOperationId> = OpenAPIOperationTypes[I]['pathParameters'];
export type OpenAPIQueryParameters<I extends OpenAPIOperationId> = OpenAPIOperationTypes[I]['queryParameters'];
export type OpenAPIHeaderParameters<I extends OpenAPIOperationId> = OpenAPIOperationTypes[I]['headerParameters'];

{chr(10).join(operation_aliases)}

type OpenAPIBodyOptions<I extends OpenAPIOperationId> = undefined extends OpenAPIRequestBody<I>
  ? {{ readonly body?: Exclude<OpenAPIRequestBody<I>, undefined> }}
  : {{ readonly body: OpenAPIRequestBody<I> }};
type OpenAPIPathOptions<I extends OpenAPIOperationId> = undefined extends OpenAPIPathParameters<I>
  ? {{ readonly pathParameters?: never }}
  : {{ readonly pathParameters: Readonly<OpenAPIPathParameters<I>> }};
type OpenAPIQueryOptions<I extends OpenAPIOperationId> = OpenAPIOperationTypes[I]['queryRequired'] extends true
  ? {{ readonly query: URLSearchParams | Readonly<OpenAPIQueryParameters<I>> }}
  : {{ readonly query?: URLSearchParams | Readonly<OpenAPIQueryParameters<I>> }};

export type OpenAPIRequestOptions<I extends OpenAPIOperationId> = OpenAPIBodyOptions<I> & OpenAPIPathOptions<I> & OpenAPIQueryOptions<I> & {{
  readonly signal?: AbortSignal;
}};

export type OpenAPITransport = <I extends OpenAPIOperationId>(
  id: I,
  options: OpenAPIRequestOptions<I>,
) => Promise<OpenAPIResponseBody<I>>;

/** Generated JSON operation facade. Streaming uploads and WebSockets use the SDK's custom adapters. */
export class OpenAPIClient {{
  constructor(private readonly transport: OpenAPITransport) {{}}

{chr(10).join(client_methods)}
}}

export function encodeOpenAPIQuery<I extends OpenAPIOperationId>(
  query?: URLSearchParams | Readonly<OpenAPIQueryParameters<I>>,
): URLSearchParams | undefined {{
  if (query == null || query instanceof URLSearchParams) return query;
  const encoded = new URLSearchParams();
  for (const [name, value] of Object.entries(query as Readonly<Record<string, unknown>>)) {{
    if (value == null) continue;
    if (name === 'search.*' && typeof value === 'object' && !Array.isArray(value)) {{
      for (const [key, item] of Object.entries(value as Readonly<Record<string, unknown>>)) {{
        if (item != null) encoded.set(`search.${{key}}`, String(item));
      }}
    }} else if (Array.isArray(value)) {{
      for (const item of value) encoded.append(name, String(item));
    }} else {{
      encoded.set(name, String(value));
    }}
  }}
  return encoded;
}}

export interface ResolvedOpenAPIOperation {{
  readonly id: OpenAPIOperationId;
  readonly method: string;
  readonly path: string;
  readonly authLane: OpenAPIAuthLane;
  readonly signing?: string;
  readonly streamingRequest: boolean;
  readonly webSocket: boolean;
  readonly requestSchema?: string;
  readonly responseSchema?: string;
}}

export function resolveOpenAPIOperation(
  id: OpenAPIOperationId,
  pathParameters: Readonly<Record<string, string>> = {{}},
  query?: URLSearchParams,
): ResolvedOpenAPIOperation {{
  const source = operationTable[id];
  let resolvedPath: string = source.path;
  for (const name of source.pathParameters) {{
    const value = pathParameters[name];
    if (value == null) {{
      throw new Error(`postgrip-agent: OpenAPI operation ${{id}} requires path parameter ${{name}}`);
    }}
    resolvedPath = resolvedPath.replace(`{{${{name}}}}`, encodeURIComponent(value));
  }}
  const encodedQuery = query?.toString();
  if (encodedQuery) resolvedPath += `?${{encodedQuery}}`;
  return {{
    id,
    method: source.method,
    path: resolvedPath,
    authLane: source.authLane,
    ...(source.signing ? {{ signing: source.signing }} : {{}}),
    streamingRequest: source.streamingRequest,
    webSocket: source.webSocket,
    ...(source.requestSchema ? {{ requestSchema: source.requestSchema }} : {{}}),
    ...(source.responseSchema ? {{ responseSchema: source.responseSchema }} : {{}}),
  }};
}}
'''


def python_source(spec: dict[str, Any], rows: list[dict[str, Any]], digest: str) -> str:
    enum_rows = "\n".join(f'    {snake_name(row["id"])} = {row["id"]!r}' for row in rows)
    table_rows = []
    for row in rows:
        params = repr(tuple(row["parameters"]))
        table_rows.append(
            f'    OperationId.{snake_name(row["id"])}: OpenAPIOperation('
            f'{row["method"]!r}, {row["path"]!r}, {row["auth"]!r}, {row["signing"]!r}, '
            f'{row["streaming"]!r}, {row["websocket"]!r}, '
            f'{schema_label(row["request_schema"])!r}, {schema_label(row["response_schema"])!r}, {params}),'
        )
    generated_types = python_types_source(spec, rows)
    generated_client = python_openapi_client_source(rows)
    custom_parameter_constants = [
        (
            f"{constant_name(row['id'])}_{constant_name(location)}_{constant_name(name)}",
            name,
        )
        for row in rows
        if row["streaming"] or row["websocket"]
        for location, parameters in (
            ("path", row["path_parameter_schemas"]),
            ("query", row["query_parameter_schemas"]),
            ("header", row["header_parameter_schemas"]),
        )
        for name in parameters
    ]
    exports = [
        "OPENAPI_SPEC_SHA256", "OPENAPI_OPERATION_COUNT", "OPENAPI_CLIENT_OPERATION_COUNT",
        "OPENAPI_OPERATION_TYPES", "OperationId", "OpenAPIClient",
        "OpenAPIOperation", "ResolvedOpenAPIOperation", "openapi_auth_lane_for_request",
        "resolve_openapi_operation",
        *[name for name, _ in custom_parameter_constants],
        *[f"OpenAPI{pascal_name(name)}" for name in spec.get("components", {}).get("schemas", {})],
        *[
            exported
            for row in rows
            for exported in (
                f"{pascal_name(row['id'])}RequestBody",
                f"{pascal_name(row['id'])}ResponseBody",
            )
        ],
    ]
    return f'''# Code generated by scripts/generate_openapi.py; DO NOT EDIT.

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
import re
from typing import Any, Literal, Mapping, Protocol, Sequence, TypeAlias, TypedDict, cast
from urllib.parse import quote, urlencode

OPENAPI_SPEC_SHA256 = {digest!r}
OPENAPI_OPERATION_COUNT = {len(rows)}
OPENAPI_CLIENT_OPERATION_COUNT = {sum(not row['streaming'] and not row['websocket'] for row in rows)}
{chr(10).join(f'{name} = {value!r}' for name, value in custom_parameter_constants)}


class OperationId(str, Enum):
{enum_rows}


{generated_types}


@dataclass(frozen=True)
class OpenAPIOperation:
    method: str
    path: str
    auth_lane: str
    signing: str
    streaming_request: bool
    websocket: bool
    request_schema: str
    response_schema: str
    path_parameters: tuple[str, ...]


@dataclass(frozen=True)
class ResolvedOpenAPIOperation:
    operation_id: OperationId
    method: str
    path: str
    auth_lane: str
    signing: str
    streaming_request: bool
    websocket: bool
    request_schema: str
    response_schema: str


_OPERATION_TABLE: dict[OperationId, OpenAPIOperation] = {{
{chr(10).join(table_rows)}
}}


{generated_client}


def openapi_auth_lane_for_request(method: str, path: str) -> str | None:
    """Return the generated auth lane for a raw request when it is known."""
    request_path = path.partition("?")[0]
    for operation in _OPERATION_TABLE.values():
        if operation.method != method.upper():
            continue
        pattern = re.sub(r"\\{{[^}}]+\\}}", r"[^/]+", operation.path)
        if re.fullmatch(pattern, request_path):
            return operation.auth_lane
    return None


def resolve_openapi_operation(
    operation_id: OperationId,
    path_parameters: Mapping[str, str] | None = None,
    query: Mapping[str, object] | Sequence[tuple[str, object]] | None = None,
) -> ResolvedOpenAPIOperation:
    source = _OPERATION_TABLE[operation_id]
    values = path_parameters or {{}}
    resolved_path = source.path
    for name in source.path_parameters:
        if name not in values:
            raise ValueError(
                f"postgrip-agent: OpenAPI operation {{operation_id.value}} "
                f"requires path parameter {{name}}"
            )
        resolved_path = resolved_path.replace("{{" + name + "}}", quote(values[name], safe=""))
    if query:
        resolved_path += "?" + urlencode(query, doseq=True)
    return ResolvedOpenAPIOperation(
        operation_id=operation_id,
        method=source.method,
        path=resolved_path,
        auth_lane=source.auth_lane,
        signing=source.signing,
        streaming_request=source.streaming_request,
        websocket=source.websocket,
        request_schema=source.request_schema,
        response_schema=source.response_schema,
    )


__all__ = {exports!r}
'''


def generated_files(spec: dict[str, Any], digest: str) -> dict[Path, str]:
    rows = operation_rows(spec)
    return {
        ROOT / "go" / "client" / "openapi_operations.gen.go": go_source(rows, digest),
        ROOT / "go" / "client" / "openapi_types.gen.go": go_types_source(spec, rows, digest),
        ROOT / "go" / "client" / "openapi_client.gen.go": go_openapi_client_source(rows, digest),
        ROOT / "typescript" / "src" / "generated" / "openapi.ts": typescript_source(spec, rows, digest),
        ROOT / "python" / "src" / "postgrip_agent" / "generated" / "__init__.py":
            "# Internal generated OpenAPI bindings.\n",
        ROOT / "python" / "src" / "postgrip_agent" / "generated" / "openapi.py":
            "# Code generated by scripts/generate_openapi.py; DO NOT EDIT.\n"
            "from ..openapi import *  # noqa: F401,F403\n",
        ROOT / "python" / "src" / "postgrip_agent" / "openapi.py": python_source(spec, rows, digest),
    }


def write_or_check(outputs: dict[Path, str], check: bool) -> bool:
    dirty: list[Path] = []
    for output_path, expected in outputs.items():
        if check:
            if not output_path.exists() or output_path.read_text() != expected:
                dirty.append(output_path.relative_to(ROOT))
            continue
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(expected)
    if dirty:
        print("Generated OpenAPI bindings are stale:", file=sys.stderr)
        for output_path in dirty:
            print(f"- {output_path}", file=sys.stderr)
        print("Run: python3 scripts/generate_openapi.py", file=sys.stderr)
        return False
    return True


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true", help="fail instead of rewriting stale output")
    parser.add_argument("--check-source", type=Path, help="verify openapi.json matches the canonical source")
    args = parser.parse_args()

    if args.check_source and SPEC_PATH.read_bytes() != args.check_source.resolve().read_bytes():
        print(f"openapi.json differs from canonical source {args.check_source}", file=sys.stderr)
        return 1

    spec, digest = load_contract()
    validate_schema_references(spec)
    outputs = generated_files(spec, digest)
    if not write_or_check(outputs, args.check):
        return 1
    action = "verified" if args.check else "generated"
    print(f"OpenAPI bindings {action}: {len(operation_rows(spec))} operations ({digest[:12]})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
