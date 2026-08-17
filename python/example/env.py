from __future__ import annotations

import os
from pathlib import Path


def load_example_env() -> None:
    for path in (Path.cwd() / ".env", Path(__file__).with_name(".env")):
        load_dotenv_file(path)


def load_dotenv_file(path: Path) -> None:
    try:
        lines = path.read_text().splitlines()
    except OSError:
        return

    for raw_line in lines:
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export ") :]
        key, sep, value = line.partition("=")
        if not sep:
            continue
        key = key.strip()
        if not key or os.environ.get(key):
            continue
        os.environ[key] = parse_dotenv_value(value)


def parse_dotenv_value(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
        return value[1:-1]
    return value
