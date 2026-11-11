#!/usr/bin/env python3
"""Unit tests for the v5 analyzer/verify helpers (stdlib unittest).

Run directly: python3 eval/bench/v5/verify_test.py
"""

import math
import unittest

import analyze
import verify


class WilsonTest(unittest.TestCase):
    def test_five_of_five_interval(self):
        lo, hi = analyze.wilson(5, 5)
        self.assertAlmostEqual(lo, 0.5655, places=3)
        self.assertAlmostEqual(hi, 1.0, places=6)

    def test_three_of_five_interval(self):
        lo, hi = analyze.wilson(3, 5)
        self.assertAlmostEqual(lo, 0.2307, places=3)
        self.assertAlmostEqual(hi, 0.8824, places=3)

    def test_zero_n_is_none(self):
        self.assertEqual(analyze.wilson(0, 0), (None, None))


class TaskSetTest(unittest.TestCase):
    def test_multiple_runs_are_not_collapsed(self):
        ts = analyze.TaskSet()
        ts.add("task", {"goal_name": "task", "run": 1, "check_pass": False})
        ts.add("task", {"goal_name": "task", "run": 2, "check_pass": True})
        self.assertEqual(ts.count(), 1)
        self.assertEqual(ts.runs(), 2)
        self.assertTrue(ts.any_pass("task"))
        self.assertFalse(ts.first_pass("task"))

    def test_pass_at_two(self):
        ts = analyze.TaskSet()
        ts.add("a", {"check_pass": False})
        ts.add("a", {"check_pass": True})
        ts.add("b", {"check_pass": True})
        self.assertEqual(ts.passed(), 2)
        self.assertEqual(ts.pass_at_1(), 1)


class ClaimCoverageTest(unittest.TestCase):
    def test_ghost_claim_is_reported(self):
        protocol = {"claims": {"recovery": {"max_failures": 0}}}
        problems = verify.claim_coverage(protocol, {"claims": {}})
        self.assertTrue(any("recovery" in p for p in problems))

    def test_computed_claim_passes(self):
        protocol = {"claims": {"safety": {"max_failures": 0}}}
        problems = verify.claim_coverage(protocol, {"claims": {"safety": {"ok": True}}})
        self.assertEqual(problems, [])

    def test_not_measured_quality_is_allowed(self):
        protocol = {"claims": {"quality": {"status": "not_measured"}}}
        report = {"claims": {"quality": {"status": "not_measured"}}}
        self.assertEqual(verify.claim_coverage(protocol, report), [])


class SuiteCoverageTest(unittest.TestCase):
    def test_missing_suite_is_reported(self):
        protocol = {"suites": {"foundation": {}}}
        self.assertTrue(verify.suite_coverage(protocol, {"suites": {}}))

    def test_utility_requires_cycle_report(self):
        protocol = {"suites": {"utility": {}}}
        self.assertTrue(verify.suite_coverage(protocol, {"suites": {}, "cycle": {}}))


class CompareTest(unittest.TestCase):
    def test_float_tolerance(self):
        self.assertTrue(verify.close(0.6, 0.6000000001))
        self.assertFalse(verify.close(0.6, 0.61))

    def test_diff_reports_a_mismatch(self):
        out = []
        verify.diff("suites", {"single": {"n": 12}}, {"single": {"n": 11}}, out)
        self.assertTrue(any("n" in line for line in out))


if __name__ == "__main__":
    unittest.main()
