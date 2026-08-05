package main

import (
	"encoding/xml"
	"fmt"
	"os"
)

// junitTestSuite/junitTestCase/junitFailure mirror the de facto JUnit XML
// schema every major CI system (GitHub Actions, GitLab CI, Jenkins) parses
// natively into a test-results view — not an official standard, but this
// shape is the one everyone's tooling actually accepts.
type junitTestSuite struct {
	XMLName   xml.Name        `xml:"testsuite"`
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Time      string          `xml:"time,attr"`
	TestCases []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// WriteJUnitReport writes results as a JUnit XML report to path, for a
// CI system to render as native test results rather than just an exit
// code. suiteName is the lab's cluster name.
func WriteJUnitReport(path, suiteName string, results []CheckResult) error {
	suite := junitTestSuite{
		Name:  suiteName,
		Tests: len(results),
	}

	var totalSeconds float64
	for _, r := range results {
		totalSeconds += r.Duration.Seconds()

		tc := junitTestCase{
			Name:      r.Name,
			ClassName: suiteName,
			Time:      fmt.Sprintf("%.3f", r.Duration.Seconds()),
		}

		if !r.Pass {
			suite.Failures++
			tc.Failure = &junitFailure{
				Message: "check failed",
				Text:    r.Message,
			}
		}

		suite.TestCases = append(suite.TestCases, tc)
	}
	suite.Time = fmt.Sprintf("%.3f", totalSeconds)

	data, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JUnit report: %w", err)
	}

	output := append([]byte(xml.Header), data...)
	if err := os.WriteFile(path, output, 0644); err != nil {
		return fmt.Errorf("failed to write JUnit report to '%s': %w", path, err)
	}

	return nil
}
