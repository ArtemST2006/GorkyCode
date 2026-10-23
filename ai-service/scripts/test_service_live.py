#!/usr/bin/env python3
"""Functional contract tests against real AI-service, E5 and Cloud.ru. No mocks."""

import argparse
import fnmatch
import importlib
import json
from pathlib import Path
import signal
import sys
import threading
import time
import unittest

import service_runtime as runtime
import test_cloudru_live as cloud

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tests/live"))
live_support = importlib.import_module("live_support")

MODULES = {"contract": "test_contract", "validation": "test_validation", "embeddings": "test_embeddings",
           "faults": "test_faults", "jobs": "test_jobs"}


class ReportResult(unittest.TextTestResult):
    def __init__(self, *args, report, flush, **kwargs):
        super().__init__(*args, **kwargs)
        self.report, self.flush = report, flush
        self.entries = {}

    def startTest(self, test):
        super().startTest(test)
        entry = dict(id=test.id(), outcome="running", started=time.monotonic(), subtests=[])
        self.entries[test.id()] = entry
        self.report["tests"].append(entry)
        self.flush()

    def addSuccess(self, test):
        super().addSuccess(test)
        entry = self.entries[test.id()]
        outcomes = [sub["outcome"] for sub in entry["subtests"]]
        entry["outcome"] = ("skipped" if outcomes and all(value == "skipped" for value in outcomes)
                            else "partial" if "skipped" in outcomes else "passed")

    def addFailure(self, test, error):
        super().addFailure(test, error)
        self.entries[test.id()].update(outcome="failed", error=str(error[1]))

    def addError(self, test, error):
        super().addError(test, error)
        self.entries[test.id()].update(outcome="error", error=type(error[1]).__name__ + ": " + str(error[1]))

    def addSkip(self, test, reason):
        super().addSkip(test, reason)
        entry = self.entries.get(test.id())
        is_subtest = hasattr(test, "test_case")
        if entry is None and is_subtest:
            entry = self.entries.get(test.test_case.id())
        if entry is not None:
            if not is_subtest:
                entry.update(outcome="skipped", reason=reason)
            else:
                entry["subtests"].append(dict(id=str(test), outcome="skipped", reason=reason))

    def addSubTest(self, test, subtest, error):
        super().addSubTest(test, subtest, error)
        entry = self.entries[test.id()]
        entry["subtests"].append(dict(id=str(subtest), outcome="failed" if error else "passed",
                                      error=str(error[1]) if error else None))
        if error:
            entry["outcome"] = "failed"
        self.flush()

    def stopTest(self, test):
        entry = self.entries[test.id()]
        entry["duration_seconds"] = round(time.monotonic() - entry.pop("started"), 3)
        if entry["outcome"] == "running":
            entry["outcome"] = "interrupted"
        self.flush()
        super().stopTest(test)


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime", choices=("host", "docker"), default="host")
    parser.add_argument("--container", type=runtime.container_name, default="gorkycode-ai-service")
    parser.add_argument("--image", default="gorkycode-ai-service:cloudru", help="image for isolated fault profiles")
    parser.add_argument("--base-url", type=cloud.service_origin)
    parser.add_argument("--host-python", type=Path)
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env")
    parser.add_argument("--timeout", type=cloud.finite_positive, default=180)
    parser.add_argument("--interval", type=cloud.interval_value, default=65)
    parser.add_argument("--sections", default=",".join(MODULES), help="comma-separated: " + ",".join(MODULES))
    parser.add_argument("--llm-suite", choices=("smoke", "core", "full"), default="full")
    parser.add_argument("--no-faults", action="store_true", help="skip temporary real service instances")
    parser.add_argument("--test", action="append", dest="test_patterns", help="select test IDs by glob (repeatable)")
    parser.add_argument("--max-cloud-requests", type=cloud.positive_int, default=100,
                        help="cap Cloud.ru HTTP attempts, excluding internal correction/transport calls")
    parser.add_argument("--list", action="store_true", help="list test IDs; do not start services or send requests")
    parser.add_argument("--report", type=Path)
    parser.add_argument("--failfast", action="store_true")
    args = parser.parse_args()
    args.sections = args.sections.split(",")
    if not set(args.sections) <= MODULES.keys():
        parser.error("unknown --sections")
    args.env_file = args.env_file.resolve()
    if args.host_python:
        args.host_python = args.host_python.absolute()
    args.mode = "api"
    args.allow_missing_key = not bool({"jobs", "faults"} & set(args.sections))
    args.report = args.report or ROOT / f"reports/service-live-{args.runtime}.json"
    return args


def flatten(suite):
    for item in suite:
        if isinstance(item, unittest.TestSuite):
            yield from flatten(item)
        else:
            yield item


def coverage_matrix(report):
    definitions = json.loads((ROOT / "tests/live/coverage.json").read_text())["requirements"]
    executed = {item["id"]: item for item in report["tests"]}
    rows = []
    for requirement in definitions:
        selected = [name for name in report["planned_tests"]
                    if any(fnmatch.fnmatchcase(name, pattern) for pattern in requirement["tests"])]
        outcomes = [executed.get(name, {}).get("outcome", "not_run") for name in selected]
        if not requirement["tests"]:
            status = "not_forced"
        elif not selected:
            status = "not_selected"
        elif any(value in {"failed", "error"} for value in outcomes):
            status = "failed"
        elif any(value in {"not_run", "running", "interrupted"} for value in outcomes):
            status = "not_run"
        elif any(value in {"skipped", "partial"} for value in outcomes) or any(
                sub["outcome"] == "skipped" for name in selected
                for sub in executed.get(name, {}).get("subtests", [])):
            status = "partial"
        else:
            status = "verified"
        rows.append(dict(id=requirement["id"], description=requirement["description"],
                         status=status, selected_tests=selected))
    return rows


def main():
    args = parse_args()
    loader = unittest.TestLoader()
    suite = unittest.TestSuite(loader.loadTestsFromModule(importlib.import_module(MODULES[section]))
                               for section in args.sections)
    if args.test_patterns:
        suite = unittest.TestSuite(test for test in flatten(suite)
                                  if any(fnmatch.fnmatchcase(test.id(), pattern) for pattern in args.test_patterns))
    if args.llm_suite != "full":
        suite = unittest.TestSuite(test for test in flatten(suite)
                                  if ".ExtractionTests." not in test.id() and (
                                      args.llm_suite != "smoke" or ".JobTests." not in test.id()
                                      or test.id().endswith("test_dialogue_create_task")))
    planned = [test.id() for test in flatten(suite)]
    if not planned:
        print("No tests matched the selected sections/patterns", file=sys.stderr)
        return 2
    if args.list:
        print("\n".join(planned))
        print(f"{len(planned)} test methods; dialogue/HTTP subtests add more checks")
        return 0
    report = dict(started_at=cloud.now(), finished_at=None, runtime=args.runtime, passed=False,
                  selected_sections=args.sections, llm_suite=args.llm_suite, tests=[],
                  planned_tests=planned, llm_responses=[], http_requests=[],
                  limitations=["Functional coverage, not Python line/branch coverage.",
                               "Upstream quota/429 and arbitrary upstream 5xx are not deliberately induced.",
                               "Malformed completion 500 is recorded only if the real provider produces it."])
    report_lock = threading.RLock()

    def flush():
        with report_lock:
            cloud.save_report(args.report, report)
    exit_code = 1
    try:
        flush()
        live = live_support.Session(args, report)
        live_support.CONTEXT = live
        live.lock = report_lock
        report["http_requests"] = live.observed
        live.flush = flush
        runner = unittest.TextTestRunner(verbosity=2, failfast=args.failfast,
                    resultclass=lambda *a, **k: ReportResult(*a, report=report, flush=flush, **k))
        result = runner.run(suite)
        report["passed"] = result.wasSuccessful()
        report["all_selected_verified"] = result.wasSuccessful() and not result.skipped
        exit_code = 0 if report["passed"] else 1
    except KeyboardInterrupt:
        report["error"] = "interrupted; in-flight Cloud.ru calls may still be billed"
        exit_code = 130
    except Exception as exc:
        # Config/bootstrap exceptions are kept free of secret values.
        report["error"] = str(exc) if isinstance(exc, (cloud.CheckFailed, AssertionError)) else type(exc).__name__
        print("FAIL " + report["error"], file=sys.stderr)
    finally:
        report["finished_at"] = cloud.now()
        report["not_run_tests"] = sorted(set(planned) - {test["id"] for test in report["tests"]})
        report["summary"] = {outcome: sum(test["outcome"] == outcome for test in report["tests"])
                             for outcome in ("passed", "partial", "failed", "error", "skipped", "interrupted", "running")}
        report["observed_http_statuses"] = sorted({item["status"] for item in report["http_requests"] if "status" in item})
        report["cloud_request_attempts"] = sum(item["paid"] for item in report["http_requests"])
        report["coverage_matrix"] = coverage_matrix(report)
        flush()
        print(f"REPORT {args.report}")
    return exit_code


if __name__ == "__main__":
    def stop_requested(signum, frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, stop_requested)
    sys.exit(main())
