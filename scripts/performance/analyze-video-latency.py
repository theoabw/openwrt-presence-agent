#!/usr/bin/env python3
"""Summarize frame-counted phone-action to Home Assistant latency."""

from __future__ import annotations

import argparse
import csv
import json
import statistics
from pathlib import Path
from typing import TypedDict


class Sample(TypedDict):
    cycle: int
    direction: str
    action_frame: int
    ha_frame: int
    fps: float
    latency_ms: float


def arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("annotations", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--label", default="phone-ui-to-ha-state")
    return parser.parse_args()


def percentile(values: list[float], fraction: float) -> float:
    ordered = sorted(values)
    index = min(len(ordered) - 1, round((len(ordered) - 1) * fraction))
    return round(ordered[index], 3)


def summary(values: list[float]) -> dict[str, float | int]:
    return {
        "sample_count": len(values),
        "median": round(statistics.median(values), 3),
        "p95": percentile(values, 0.95),
        "p99": percentile(values, 0.99),
        "maximum": round(max(values), 3),
    }


def read_samples(path: Path) -> list[Sample]:
    samples: list[Sample] = []
    with path.open(newline="") as source:
        for row_number, row in enumerate(csv.DictReader(source), start=2):
            try:
                cycle = int(row["cycle"])
                direction = row["direction"].strip()
                action_frame = int(row["action_frame"])
                ha_frame = int(row["ha_frame"])
                fps = float(row["fps"])
            except (KeyError, TypeError, ValueError) as error:
                raise ValueError(f"invalid annotation row {row_number}") from error
            if direction not in {"off", "on"}:
                raise ValueError(f"row {row_number}: direction must be off or on")
            if cycle < 1 or action_frame < 0 or ha_frame < action_frame or fps <= 0:
                raise ValueError(f"row {row_number}: invalid frame or FPS value")
            samples.append(
                {
                    "cycle": cycle,
                    "direction": direction,
                    "action_frame": action_frame,
                    "ha_frame": ha_frame,
                    "fps": fps,
                    "latency_ms": round((ha_frame - action_frame) * 1000 / fps, 3),
                }
            )
    if not samples:
        raise ValueError("annotation file has no samples")
    return samples


def main() -> None:
    args = arguments()
    samples = read_samples(args.annotations)
    frame_durations = sorted({round(1000 / sample["fps"], 3) for sample in samples})
    by_direction = {
        direction: summary(
            [
                sample["latency_ms"]
                for sample in samples
                if sample["direction"] == direction
            ]
        )
        for direction in ("off", "on")
        if any(sample["direction"] == direction for sample in samples)
    }
    result = {
        "schema_version": 1,
        "label": args.label,
        "metric": "phone_wifi_ui_to_home_assistant_visible_state",
        "method": "single-camera frame counting",
        "measurement_resolution_ms_per_frame": frame_durations,
        "summary_ms": summary([sample["latency_ms"] for sample in samples]),
        "direction_summary_ms": by_direction,
        "samples": samples,
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result["summary_ms"], sort_keys=True))


if __name__ == "__main__":
    main()
