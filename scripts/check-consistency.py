#!/usr/bin/env python3
"""Check duplicated release and protocol metadata for drift."""

from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def match(path: str, pattern: str) -> str:
    text = (ROOT / path).read_text()
    found = re.search(pattern, text, flags=re.MULTILINE)
    if found is None:
        raise SystemExit(f"{path}: expected pattern not found: {pattern}")
    return found.group(1)


package_version = match("packaging/openwrt/Makefile", r"^PKG_VERSION:=(\S+)$")
if re.fullmatch(r"\d+\.\d+\.\d+", package_version) is None:
    raise SystemExit(f"invalid package version: {package_version}")

protocol_version = match("pkg/protocol/types.go", r'^const Version = "(v\d+)"$')
schema = json.loads((ROOT / "api/protocol.schema.json").read_text())
fixture = json.loads((ROOT / "api/fixtures/v1.json").read_text())
openapi = (ROOT / "api/openapi.yaml").read_text()

if schema.get("title") != f"OpenWrt Presence Agent {protocol_version} protocol":
    raise SystemExit("JSON Schema title does not match the Go protocol version")
if fixture.get("protocol_version") != protocol_version:
    raise SystemExit("contract fixture does not match the Go protocol version")
if fixture.get("info", {}).get("protocol_version") != protocol_version:
    raise SystemExit("fixture info does not match the Go protocol version")
if f"protocol_version: {{ const: {protocol_version} }}" not in openapi:
    raise SystemExit("OpenAPI info schema does not match the Go protocol version")
if f"version: {protocol_version.removeprefix('v')}.0.0" not in openapi:
    raise SystemExit("OpenAPI document version does not match the protocol version")

print(
    f"consistent package_version={package_version} "
    f"protocol_version={protocol_version}"
)
