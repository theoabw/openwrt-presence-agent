#!/usr/bin/env python3
"""Measure observer-event to Home Assistant state-event latency.

Run this from a Home Assistant add-on or another trusted host with aiohttp.
Secrets are accepted only through an environment variable and a protected file.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import statistics
import time
from collections import defaultdict, deque
from pathlib import Path
from typing import Any

import aiohttp


def arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--observer-url", required=True)
    parser.add_argument("--observer-token-file", type=Path, required=True)
    parser.add_argument("--ha-url", default="ws://supervisor/core/api/websocket")
    parser.add_argument("--ha-token-env", default="SUPERVISOR_TOKEN")
    parser.add_argument("--entity-id", required=True)
    parser.add_argument("--client-id")
    parser.add_argument("--label", default="test-client")
    parser.add_argument("--transitions", type=int, default=10)
    parser.add_argument("--timeout", type=float, default=600)
    parser.add_argument("--output", type=Path, required=True)
    return parser.parse_args()


async def receive_json(socket: aiohttp.ClientWebSocketResponse) -> dict[str, Any]:
    message = await socket.receive()
    if message.type is not aiohttp.WSMsgType.TEXT:
        raise RuntimeError(f"WebSocket closed unexpectedly: {message.type}")
    value = json.loads(message.data)
    if not isinstance(value, dict):
        raise TypeError("WebSocket message is not an object")
    return value


async def authenticate_ha(
    session: aiohttp.ClientSession, url: str, token: str
) -> aiohttp.ClientWebSocketResponse:
    socket = await session.ws_connect(url, heartbeat=30)
    required = await receive_json(socket)
    if required.get("type") != "auth_required":
        raise RuntimeError("Home Assistant did not request authentication")
    await socket.send_json({"type": "auth", "access_token": token})
    result = await receive_json(socket)
    if result.get("type") != "auth_ok":
        raise RuntimeError("Home Assistant WebSocket authentication failed")
    return socket


async def request(
    socket: aiohttp.ClientWebSocketResponse,
    request_id: int,
    request_type: str,
    **fields: Any,
) -> Any:
    await socket.send_json({"id": request_id, "type": request_type, **fields})
    while True:
        response = await receive_json(socket)
        if response.get("id") == request_id:
            if not response.get("success"):
                raise RuntimeError(f"Home Assistant request failed: {response}")
            return response.get("result")


def client_id_from_state(state: dict[str, Any]) -> str | None:
    attributes = state.get("attributes")
    if not isinstance(attributes, dict):
        return None
    address = attributes.get("mac")
    if not isinstance(address, str):
        return None
    parts = address.lower().split(":")
    if len(parts) != 6 or any(len(part) != 2 for part in parts):
        return None
    return f"mac:{address.lower()}"


def expected_ha_state(observer_state: str) -> str:
    return {
        "present": "home",
        "absent": "not_home",
        "unknown": "unknown",
    }[observer_state]


async def run(args: argparse.Namespace) -> dict[str, Any]:
    ha_token = os.environ.get(args.ha_token_env)
    if not ha_token:
        raise RuntimeError(f"{args.ha_token_env} is not set")
    observer_token = args.observer_token_file.read_text().strip()
    if not observer_token:
        raise RuntimeError("observer token file is empty")

    timeout = aiohttp.ClientTimeout(total=None, connect=10)
    async with aiohttp.ClientSession(timeout=timeout) as session:
        ha = await authenticate_ha(session, args.ha_url, ha_token)
        states = await request(ha, 1, "get_states")
        entity = next(
            (
                state
                for state in states
                if state.get("entity_id") == args.entity_id
            ),
            None,
        )
        if entity is None:
            raise RuntimeError(f"entity does not exist: {args.entity_id}")
        client_id = args.client_id or client_id_from_state(entity)
        if client_id is None:
            raise RuntimeError("could not derive client ID; pass --client-id")

        await ha.send_json(
            {"id": 2, "type": "subscribe_events", "event_type": "state_changed"}
        )
        subscribed = await receive_json(ha)
        if subscribed.get("id") != 2 or not subscribed.get("success"):
            raise RuntimeError("could not subscribe to Home Assistant state events")

        observer = await session.ws_connect(
            args.observer_url,
            headers={"Authorization": f"Bearer {observer_token}"},
            heartbeat=30,
            max_msg_size=2 * 1024 * 1024,
        )
        hello = await receive_json(observer)
        snapshot = await receive_json(observer)
        if hello.get("type") != "stream.hello" or snapshot.get("type") != "state.snapshot":
            raise RuntimeError("observer stream did not begin with hello and snapshot")

        pending: dict[str, deque[float]] = defaultdict(deque)
        samples: list[dict[str, Any]] = []
        print(
            f"READY entity={args.entity_id} current={entity.get('state')} "
            f"target_transitions={args.transitions}",
            flush=True,
        )

        async def observer_events() -> None:
            async for message in observer:
                if message.type is not aiohttp.WSMsgType.TEXT:
                    break
                received = time.monotonic()
                event = json.loads(message.data)
                if event.get("type") != "client.presence_changed":
                    continue
                data = event.get("data")
                if not isinstance(data, dict) or data.get("id") != client_id:
                    continue
                source_state = data.get("state")
                if source_state not in {"present", "absent", "unknown"}:
                    continue
                state = expected_ha_state(source_state)
                pending[state].append(received)
                print(f"OBSERVER state={source_state}", flush=True)

        async def ha_events() -> None:
            async for message in ha:
                if message.type is not aiohttp.WSMsgType.TEXT:
                    break
                received = time.monotonic()
                envelope = json.loads(message.data)
                event = envelope.get("event")
                data = event.get("data") if isinstance(event, dict) else None
                if not isinstance(data, dict) or data.get("entity_id") != args.entity_id:
                    continue
                new_state = data.get("new_state")
                if not isinstance(new_state, dict):
                    continue
                state = new_state.get("state")
                if state not in pending or not pending[state]:
                    continue
                latency_ms = (received - pending[state].popleft()) * 1000
                samples.append(
                    {
                        "transition": state,
                        "latency_ms": round(latency_ms, 3),
                    }
                )
                print(
                    f"HOME_ASSISTANT state={state} latency_ms={latency_ms:.3f} "
                    f"sample={len(samples)}/{args.transitions}",
                    flush=True,
                )

        tasks = [
            asyncio.create_task(observer_events()),
            asyncio.create_task(ha_events()),
        ]
        try:
            async with asyncio.timeout(args.timeout):
                while len(samples) < args.transitions:
                    await asyncio.sleep(0.05)
        finally:
            for task in tasks:
                task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
            await observer.close()
            await ha.close()

    values = [sample["latency_ms"] for sample in samples]
    ordered = sorted(values)

    def percentile(fraction: float) -> float:
        index = min(len(ordered) - 1, round((len(ordered) - 1) * fraction))
        return round(ordered[index], 3)

    return {
        "schema_version": 1,
        "label": args.label,
        "metric": "observer_websocket_to_ha_state_event",
        "clock": "single_process_monotonic",
        "sample_count": len(samples),
        "summary_ms": {
            "median": round(statistics.median(values), 3),
            "p95": percentile(0.95),
            "p99": percentile(0.99),
            "maximum": round(max(values), 3),
        },
        "samples": samples,
    }


async def main() -> None:
    args = arguments()
    result = await run(args)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result["summary_ms"], sort_keys=True), flush=True)


if __name__ == "__main__":
    asyncio.run(main())
