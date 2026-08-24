#!/usr/bin/env python3
"""Generate a Markdown report from SKU Mix Placement logs."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


RESPONSE_MESSAGE = "received SKU Mix Placement API response"
ORDER_MESSAGE = "sorted non-first SKU Mix Placement choice first"
COMPARISON_MESSAGE = "compared SKU Mix Placement recommendations with local ranking"
TARGET_MESSAGES = {RESPONSE_MESSAGE, ORDER_MESSAGE, COMPARISON_MESSAGE}
CAPACITY_ORDER = {"on-demand": 0, "spot": 1}
SCOPE_ORDER = {"zonal": 0, "regional": 1}


@dataclass
class Record:
    line: int
    data: dict[str, Any]


@dataclass
class Response:
    record: Record
    body: dict[str, Any]
    choice_ids: set[str]
    comparison: Record | None = None
    order_error: Record | None = None


@dataclass
class Call:
    key: tuple[str, str]
    records: list[Record] = field(default_factory=list)
    responses: list[Response] = field(default_factory=list)
    comparisons: list[Record] = field(default_factory=list)
    order_errors: list[Record] = field(default_factory=list)

    @property
    def timestamp(self) -> str:
        return min((record.data.get("time", "") for record in self.records), default="unknown")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("log", type=Path, help="raw E2E log file")
    parser.add_argument("--output", "-o", type=Path, help="write Markdown to this file")
    return parser.parse_args()


def extract_record(line: str, line_number: int) -> Record | None:
    start = line.find('{"level"')
    if start < 0:
        return None
    try:
        data, _ = json.JSONDecoder().raw_decode(line[start:])
    except json.JSONDecodeError as error:
        if any(message in line for message in TARGET_MESSAGES):
            raise ValueError(f"line {line_number}: invalid target JSON: {error}") from error
        return None
    if data.get("message") not in TARGET_MESSAGES:
        return None
    return Record(line_number, data)


def call_key(data: dict[str, Any]) -> tuple[str, str]:
    node_claim = data.get("NodeClaim")
    if isinstance(node_claim, dict):
        node_claim = node_claim.get("name", "")
    if not node_claim:
        node_claim = data.get("name", "")
    return str(node_claim), str(data.get("reconcileID", ""))


def parse_embedded_json(value: Any, description: str) -> dict[str, Any]:
    if isinstance(value, dict):
        return value
    if not isinstance(value, str):
        raise ValueError(f"{description} is not JSON text")
    parsed = json.loads(value)
    if not isinstance(parsed, dict):
        raise ValueError(f"{description} is not a JSON object")
    return parsed


def load_calls(path: Path) -> tuple[list[Call], list[str]]:
    calls_by_key: dict[tuple[str, str], Call] = {}
    warnings: list[str] = []
    with path.open(encoding="utf-8", errors="replace") as log_file:
        for line_number, line in enumerate(log_file, 1):
            record = extract_record(line, line_number)
            if record is None:
                continue
            key = call_key(record.data)
            call = calls_by_key.setdefault(key, Call(key))
            call.records.append(record)
            message = record.data["message"]
            if message == RESPONSE_MESSAGE:
                try:
                    body = parse_embedded_json(record.data.get("response"), f"line {line_number} response")
                except (ValueError, json.JSONDecodeError) as error:
                    warnings.append(str(error))
                    body = {}
                choice_ids = {
                    str(choice.get("id"))
                    for choice in body.get("placementChoices", [])
                    if isinstance(choice, dict) and choice.get("id") is not None
                }
                call.responses.append(Response(record, body, choice_ids))
            elif message == ORDER_MESSAGE:
                call.order_errors.append(record)
            else:
                call.comparisons.append(record)

    calls = sorted(calls_by_key.values(), key=lambda call: (call.timestamp, call.key))
    for call in calls:
        associate_records(call, warnings)
    return calls, warnings


def associate_records(call: Call, warnings: list[str]) -> None:
    scopes: dict[tuple[str, str], list[int]] = defaultdict(list)
    for comparison in call.comparisons:
        scope = (
            str(comparison.data.get("capacityType", "unknown")),
            str(comparison.data.get("placementScope", "unknown")),
        )
        scopes[scope].append(comparison.line)
        split_id = str(comparison.data.get("splitID", ""))
        candidates = [response for response in call.responses if split_id in response.choice_ids]
        if len(candidates) == 1 and candidates[0].comparison is None:
            candidates[0].comparison = comparison
        elif len(candidates) == 1:
            warnings.append(
                f"line {comparison.line}: response already has a comparison at line "
                f"{candidates[0].comparison.line} in call {call.key}"
            )
        elif len(candidates) == 0:
            warnings.append(
                f"line {comparison.line}: comparison splitID {split_id!r} has no raw response "
                f"in call {call.key} (possibly a cache hit)"
            )
        else:
            warnings.append(
                f"line {comparison.line}: comparison splitID {split_id!r} matches multiple responses "
                f"in call {call.key}"
            )

    for scope, lines in scopes.items():
        if len(lines) > 1:
            warnings.append(f"call {call.key}: duplicate comparison scope {scope} on lines {lines}")

    for order_error in call.order_errors:
        timestamp = order_error.data.get("time")
        candidates = [
            response
            for response in call.responses
            if response.record.data.get("time") == timestamp and response.order_error is None
        ]
        if len(candidates) != 1:
            preceding = [
                response
                for response in call.responses
                if response.record.line < order_error.line and response.order_error is None
            ]
            candidates = preceding[-1:] if preceding else []
        if len(candidates) == 1:
            candidates[0].order_error = order_error
        else:
            warnings.append(f"line {order_error.line}: ordering error has no unambiguous raw response")

    for response in call.responses:
        if response.comparison is None:
            warnings.append(f"line {response.record.line}: raw response has no associated comparison")


def scope_label(record: Record | None) -> str:
    if record is None:
        return "unknown capacity type and scope"
    return f"{record.data.get('capacityType', 'unknown')}, {record.data.get('placementScope', 'unknown')}"


def choice_summary(value: Any) -> str:
    try:
        choice = parse_embedded_json(value, "placement choice")
    except (ValueError, json.JSONDecodeError):
        return "unknown choice"
    split = choice.get("skuSplit", [])
    sku_names = list(dict.fromkeys(
        str(item.get("name")) for item in split if isinstance(item, dict) and item.get("name")
    ))
    zones = list(dict.fromkeys(
        str(item.get("zone")) for item in split if isinstance(item, dict) and item.get("zone")
    ))
    summary = ", ".join(sku_names) if sku_names else f"choice {choice.get('id', 'unknown')}"
    if zones:
        summary += f" in zone(s) {', '.join(zones)}"
    return summary


def choice_skus(value: Any) -> tuple[str, ...]:
    try:
        choice = parse_embedded_json(value, "placement choice")
    except (ValueError, json.JSONDecodeError):
        return ("unknown choice",)
    return tuple(dict.fromkeys(
        str(item.get("name"))
        for item in choice.get("skuSplit", [])
        if isinstance(item, dict) and item.get("name")
    )) or ("unknown choice",)


def ranking_issues(record: Record) -> tuple[list[tuple[tuple[Any, ...], str]], bool]:
    local = record.data.get("localRanking", [])
    recommended = record.data.get("recommendedRanking", [])
    if local == recommended:
        return [], False

    local_by_vm = {item.get("vmSize"): item for item in local if isinstance(item, dict)}
    recommended_by_vm = {item.get("vmSize"): item for item in recommended if isinstance(item, dict)}
    issues: list[tuple[tuple[Any, ...], str]] = []
    for vm_size, local_item in local_by_vm.items():
        local_zones = set(map(str, local_item.get("zones") or []))
        recommended_item = recommended_by_vm.get(vm_size)
        if recommended_item is None:
            zone_suffix = f" (zones {', '.join(sorted(local_zones))})" if local_zones else ""
            zones = tuple(sorted(local_zones))
            issues.append((("removed", vm_size, zones), f"{vm_size}{zone_suffix} filtered entirely"))
            continue
        recommended_zones = set(map(str, recommended_item.get("zones") or []))
        missing_zones = sorted(local_zones - recommended_zones)
        if missing_zones:
            zones = tuple(missing_zones)
            issues.append((
                ("zones", vm_size, zones),
                f"{vm_size} filtered from zone(s) {', '.join(missing_zones)}",
            ))

    local_order = [item.get("vmSize") for item in local if isinstance(item, dict)]
    recommended_order = [item.get("vmSize") for item in recommended if isinstance(item, dict)]
    surviving_order = [vm_size for vm_size in local_order if vm_size in recommended_by_vm]
    additions = [vm_size for vm_size in recommended_order if vm_size not in local_by_vm]
    for vm_size in additions:
        issues.append((("addition", vm_size), f"{vm_size} unexpectedly added"))

    order_differs = recommended_order != surviving_order
    if order_differs:
        issues.append((
            ("order", tuple(surviving_order), tuple(recommended_order)),
            "genuine order difference: expected surviving order "
            + " → ".join(map(str, surviving_order))
            + "; recommended order "
            + " → ".join(map(str, recommended_order)),
        ))
    return issues, not order_differs and not additions


def ranking_summary(record: Record) -> str:
    issues, expected_compaction = ranking_issues(record)
    if not issues:
        return "✅ Match; nothing filtered."

    details = [description for _, description in issues]
    if expected_compaction:
        details.append("remaining rank changes are expected compaction after removals")
    return "❌ " + "; ".join(details) + "."


def ranking_evidence(record: Record) -> str:
    data = record.data
    lines = ["{"]
    lines.append(f'  "capacityType": {json.dumps(data.get("capacityType", "unknown"))},')
    lines.append(f'  "placementScope": {json.dumps(data.get("placementScope", "unknown"))},')
    for field_name in ("localRanking", "recommendedRanking"):
        ranking = data.get(field_name, [])
        lines.append(f'  "{field_name}": [')
        for index, item in enumerate(ranking):
            suffix = "," if index < len(ranking) - 1 else ""
            lines.append(f"    {json.dumps(item, sort_keys=False)}{suffix}")
        field_suffix = "," if field_name == "localRanking" else ""
        lines.append(f"  ]{field_suffix}")
    lines.append("}")
    return "\n".join(lines)


def vm_family(vm_size: Any) -> str:
    name = str(vm_size)
    match = re.match(r"^(Standard_[A-Za-z]+)\d+(?:-\d+)?(.*)$", name)
    if match is None:
        return name
    return f"{match.group(1)}{match.group(2)}"


def family_sequence(vm_sizes: tuple[Any, ...]) -> tuple[str, ...]:
    return tuple(dict.fromkeys(vm_family(vm_size) for vm_size in vm_sizes))


def summary_ranking_issue(
    key: tuple[Any, ...],
) -> tuple[tuple[Any, ...], str, str | None] | None:
    issue_type = key[0]
    if issue_type in {"removed", "zones"}:
        vm_size, zones = str(key[1]), key[2]
        family = vm_family(vm_size)
        if issue_type == "removed":
            zone_suffix = f" in zones {', '.join(zones)}" if zones else ""
            description = f"`{family}` family filtered entirely{zone_suffix}"
        else:
            description = f"`{family}` family filtered from zone(s) {', '.join(zones)}"
        return (issue_type, family, zones), description, vm_size
    if issue_type == "addition":
        vm_size = str(key[1])
        family = vm_family(vm_size)
        return (issue_type, family), f"`{family}` family unexpectedly added", vm_size
    if issue_type == "order":
        surviving_families = family_sequence(key[1])
        recommended_families = family_sequence(key[2])
        if surviving_families == recommended_families:
            return None
        description = (
            "genuine family order difference: expected surviving family order "
            + " → ".join(surviving_families)
            + "; recommended family order "
            + " → ".join(recommended_families)
        )
        return (issue_type, surviving_families, recommended_families), description, None
    return key, "unknown ranking difference", None


def comparison_sort_key(record: Record) -> tuple[int, int, str]:
    data = record.data
    return (
        CAPACITY_ORDER.get(str(data.get("capacityType")), 99),
        SCOPE_ORDER.get(str(data.get("placementScope")), 99),
        str(data.get("time", "")),
    )


def render_call(call: Call, number: int) -> str:
    node_claim, reconcile_id = call.key
    lines = [f"### Call {number}: {call.timestamp}", ""]
    first_response = call.responses[0] if call.responses else None
    choice_id = first_choice_id(first_response) or "unavailable (no raw response)"
    lines.append(
        f"NodeClaim: `{node_claim or 'unknown'}`; reconcileID: `{reconcile_id or 'unknown'}`; "
        f"first placement choice UUID: `{choice_id}`"
    )
    lines.append("")

    failures = [response for response in call.responses if response.order_error is not None]
    unattached_errors = len(call.order_errors) - len(failures)
    if failures or unattached_errors:
        labels = ", ".join(f"`({scope_label(response.comparison)})`" for response in failures)
        suffix = f" in {labels}" if labels else ""
        lines.append(f"**Placement API response order vs expected priority order:** ❌ Incorrect{suffix}.")
    else:
        cache_note = " No raw response was emitted for this cached result." if not call.responses else ""
        lines.append(f"**Placement API response order vs expected priority order:** ✅ Correct.{cache_note}")
    lines.append("")

    for comparison in sorted(call.comparisons, key=comparison_sort_key):
        label = scope_label(comparison)
        lines.append(f"**Placement API compared with local ranking ({label}):** {ranking_summary(comparison)}")
        lines.append("")
        issues, _ = ranking_issues(comparison)
        if issues:
            lines.append("Requested local ranking and received recommendation:")
            lines.append("")
            lines.append("```json")
            lines.append(ranking_evidence(comparison))
            lines.append("```")
            lines.append("")

    for index, response in enumerate(failures, 1):
        error = response.order_error.data
        expected = error.get("ourChoice", error.get("topChoice"))
        actual = error.get("placementChoice", error.get("firstChoice"))
        lines.append(f"#### Ordering failure {index}: {scope_label(response.comparison)}")
        lines.append("")
        lines.append(f"The API returned **{choice_summary(actual)}** first; local priority selected **{choice_summary(expected)}**.")
        lines.append("")
        lines.append("Full placement API response:")
        lines.append("")
        lines.append("```json")
        lines.append(json.dumps(response.body, indent=2, sort_keys=False))
        lines.append("```")
        lines.append("")
    return "\n".join(lines).rstrip()


def response_for_comparison(call: Call, comparison: Record) -> Response | None:
    return next((response for response in call.responses if response.comparison is comparison), None)


def first_choice_id(response: Response | None) -> str | None:
    if response is None:
        return None
    choices = response.body.get("placementChoices", [])
    if not choices or not isinstance(choices[0], dict) or choices[0].get("id") is None:
        return None
    return str(choices[0]["id"])


def example_label(number: int, call: Call, response: Response | None) -> str:
    node_claim, _ = call.key
    details = f"{call.timestamp}, NodeClaim `{node_claim or 'unknown'}`"
    if choice_id := first_choice_id(response):
        details += f", first placement choice UUID `{choice_id}`"
    return f"Call {number} ({details})"


def render_summary(calls: list[Call]) -> str:
    numbered_calls = list(enumerate(calls, 1))
    ordering_groups: dict[
        tuple[str, tuple[str, ...], tuple[str, ...]],
        list[tuple[int, Call, Response]],
    ] = defaultdict(list)
    ranking_groups: dict[
        str,
        dict[tuple[Any, ...], list[tuple[int, Call, Response | None, str, str | None]]],
    ] = defaultdict(lambda: defaultdict(list))
    scope_totals: dict[str, int] = defaultdict(int)
    scope_differences: dict[str, int] = defaultdict(int)

    for number, call in numbered_calls:
        for response in call.responses:
            if response.order_error is None:
                continue
            error = response.order_error.data
            expected = error.get("ourChoice", error.get("topChoice"))
            actual = error.get("placementChoice", error.get("firstChoice"))
            key = (scope_label(response.comparison), choice_skus(actual), choice_skus(expected))
            ordering_groups[key].append((number, call, response))

        for comparison in call.comparisons:
            scope = scope_label(comparison)
            response = response_for_comparison(call, comparison)
            scope_totals[scope] += 1
            issues, _ = ranking_issues(comparison)
            summary_issues: dict[tuple[Any, ...], tuple[str, str | None]] = {}
            for key, _ in issues:
                summarized = summary_ranking_issue(key)
                if summarized is None:
                    continue
                summary_key, description, example_size = summarized
                summary_issues.setdefault(summary_key, (description, example_size))
            if summary_issues:
                scope_differences[scope] += 1
            for key, (description, example_size) in summary_issues.items():
                ranking_groups[scope][key].append((number, call, response, description, example_size))

    lines = ["## Summary", ""]
    response_count = sum(len(call.responses) for call in calls)
    comparison_count = sum(len(call.comparisons) for call in calls)
    ordering_failure_count = sum(len(group) for group in ordering_groups.values())
    lines.append(
        f"Analyzed {len(calls)} calls, {response_count} emitted API responses, and "
        f"{comparison_count} ranking comparisons. Found {ordering_failure_count} response ordering "
        f"failure(s) across {len(ordering_groups)} unique pattern(s)."
    )
    lines.extend(["", "### Placement API response order vs expected priority order", ""])

    if not ordering_groups:
        lines.extend(["✅ No response ordering issues found.", ""])
    else:
        sorted_groups = sorted(ordering_groups.items(), key=lambda item: item[0])
        for index, ((scope, actual_skus, expected_skus), occurrences) in enumerate(sorted_groups, 1):
            number, call, response = occurrences[0]
            actual_label = ", ".join(f"`{sku}`" for sku in actual_skus)
            expected_label = ", ".join(f"`{sku}`" for sku in expected_skus)
            lines.append(f"#### Ordering issue {index}: {scope}")
            lines.append("")
            lines.append(
                f"❌ The API placed {actual_label} ahead of locally preferred {expected_label} in "
                f"{len(occurrences)} response(s). Example: {example_label(number, call, response)}."
            )
            lines.append("")

    lines.extend(["### Placement API compared with local ranking", ""])
    all_scopes = sorted(
        scope_totals,
        key=lambda scope: (
            CAPACITY_ORDER.get(scope.split(", ", 1)[0], 99),
            SCOPE_ORDER.get(scope.split(", ", 1)[-1], 99),
            scope,
        ),
    )
    for scope in all_scopes:
        difference_count = scope_differences[scope]
        total = scope_totals[scope]
        lines.append(f"#### {scope}")
        lines.append("")
        if difference_count == 0:
            lines.extend([f"✅ All {total} comparison(s) matched the local ranking.", ""])
            continue
        unique_issues = ranking_groups[scope]
        lines.append(
            f"❌ {difference_count} of {total} comparison(s) differed, representing "
            f"{len(unique_issues)} unique issue(s):"
        )
        lines.append("")
        for occurrences in unique_issues.values():
            number, call, response, description, example_size = occurrences[0]
            occurrence_count = len({number for number, _, _, _, _ in occurrences})
            example_prefix = f" Example size `{example_size}`." if example_size else ""
            lines.append(
                f"- {description}; observed in {occurrence_count} call(s).{example_prefix} "
                f"Example: {example_label(number, call, response)}."
            )
        lines.append("")

    return "\n".join(lines).rstrip()


def render_report(calls: list[Call]) -> str:
    sections = ["# SKU Mix Placement Analysis", render_summary(calls), "## Individual Calls"]
    sections.extend(render_call(call, index) for index, call in enumerate(calls, 1))
    return "\n\n".join(sections) + "\n"


def main() -> int:
    args = parse_args()
    try:
        calls, warnings = load_calls(args.log)
    except (OSError, ValueError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    if not calls:
        print("error: no SKU Mix Placement records found", file=sys.stderr)
        return 1

    report = render_report(calls)
    if args.output:
        args.output.write_text(report, encoding="utf-8")
        print(f"wrote {len(calls)} call(s) to {args.output}", file=sys.stderr)
    else:
        print(report, end="")
    for warning in warnings:
        print(f"warning: {warning}", file=sys.stderr)
    print(
        f"analyzed {sum(len(call.responses) for call in calls)} responses, "
        f"{sum(len(call.comparisons) for call in calls)} comparisons, and "
        f"{sum(len(call.order_errors) for call in calls)} ordering errors",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
