package parser

import (
	"bufio"
	"encoding/json"
	"io"
	"path"
	"regexp"
	"time"
	"fmt"
)

// Result represents a test result.
type Result int

// Test result constants
const (
	PASS Result = iota
	FAIL
	SKIP
)

// Report is a collection of package tests.
type Report struct {
	Packages []Package
}

// Package contains the test results of a single package.
type Package struct {
	Name        string
	Duration    time.Duration
	Tests       []*Test
	Benchmarks  []*Benchmark
	CoveragePct string

	// Time is deprecated, use Duration instead.
	Time int // in milliseconds
}

// Test contains the results of a single test.
type Test struct {
	Name     string
	Duration time.Duration
	Result   Result
	Output   []string
	Failure  []string
	SkipMsg  []string

	SubtestIndent string

	// Time is deprecated, use Duration instead.
	Time int // in milliseconds
}

type Info struct {
	Suite string
	Test  string
	Msg   string
	Time  string
}


// Benchmark contains the results of a single benchmark.
type Benchmark struct {
	Name     string
	Duration time.Duration
	// number of B/op
	Bytes int
	// number of allocs/op
	Allocs int
}

var (
	regexStatus   = regexp.MustCompile(`--- (PASS|FAIL|SKIP): (?:(.+)\/(.+)\/)?(.+) \((\d+\.\d+)(?: seconds|s)\)`)
	//regexIndent   = regexp.MustCompile(`^([ \t]+)---`)
	//regexCoverage = regexp.MustCompile(`^coverage:\s+(\d+\.\d+)%\s+of\s+statements(?:\sin\s.+)?$`)
	regexResult   = regexp.MustCompile(`^(ok|FAIL)\s+([^ ]+)\s+(?:(\d+\.\d+)s|\(cached\)|(\[\w+ failed]))(?:\s+coverage:\s+(\d+\.\d+)%\sof\sstatements(?:\sin\s.+)?)?$`)
	// regexBenchmark captures 3-5 groups: benchmark name, number of times ran, ns/op (with or without decimal), B/op (optional), and allocs/op (optional).
	//regexBenchmark = regexp.MustCompile(`^(Benchmark[^ -]+)(?:-\d+\s+|\s+)(\d+)\s+(\d+|\d+\.\d+)\sns/op(?:\s+(\d+)\sB/op)?(?:\s+(\d+)\sallocs/op)?`)
	//regexOutput    = regexp.MustCompile(`(    )*\t(.*)`)
	//regexSummary   = regexp.MustCompile(`^(PASS|FAIL|SKIP)$`)
	regexTestStart = regexp.MustCompile(`STARTING E2E STAGE:\s+(\w+)`)
)

// Parse parses go test output from reader r and returns a report with the
// results. An optional pkgName can be given, which is used in case a package
// result line is missing.
func Parse(r io.Reader, pkgName string) (*Report, error) {
	reader := bufio.NewReader(r)

	report := &Report{make([]Package, 0)}

	var suites = make(map[string]map[string]*Test)
	var initSuite = make(map[string]*Test)

	var cur *Test

	var lastSeenStage string

	// parse lines
	for {
		l, _, err := reader.ReadLine()
		if err != nil && err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		line := string(l)

		var info Info


		if matches := regexResult.FindStringSubmatch(line); len(matches) == 6 {
			for suite, testmap := range suites {
				var finalTests []*Test
				var suiteTime = time.Duration(0)
				for _, testinfo := range testmap {
					finalTests = append(finalTests, testinfo)
					suiteTime += testinfo.Duration
				}
				report.Packages = append(report.Packages, Package{
					Name:     suite,
					Duration: suiteTime,
					Time:     int(suiteTime / time.Millisecond),
					Tests:    finalTests,
				})
			}
			suites = make(map[string]map[string]*Test)
		} else if matches := regexStatus.FindStringSubmatch(line); len(matches) == 6 {
			curSuite := path.Base(matches[3])
			curTest := path.Base(matches[4])
			var testdata *Test = nil
			for _, testmap := range suites {
				for test, testInfo := range testmap {
					if test == curTest {
						testdata = testInfo
						break
					}
				}
				if testdata != nil {
					break
				}
			}

			if testdata == nil {
				stageInitTestName := fmt.Sprintf("%s_%s", lastSeenStage, curTest)
				if testInfo, ok := initSuite[stageInitTestName]; ok {
					testdata = testInfo
				}
			}

			if testdata == nil {
				if testmap, mapok := suites[curSuite]; mapok {
					if _, ok := testmap[curTest]; !ok {
						t := &Test{
							Name: curTest,
						}
						testmap[curTest] = t
					}
				} else {
					t := &Test{
						Name: curTest,
					}
					m := make(map[string]*Test)
					m[curTest] = t
					suites[curSuite] = m
				}
				testdata = suites[curSuite][curTest]
			}

			// test status
			if matches[1] == "PASS" {
				testdata.Result = PASS
			} else if matches[1] == "SKIP" {
				testdata.Result = SKIP
			} else {
				testdata.Result = FAIL
			}

			testdata.Duration = parseSeconds(matches[5])
			cur = testdata
		} else if matches := regexTestStart.FindStringSubmatch(line); len(matches) == 2 {
			testName := fmt.Sprintf("%s_TestGoe2e", matches[1])
			if _, ok := initSuite[testName]; !ok {
				t := &Test{
					Name: testName,
				}
				initSuite[testName] = t
				lastSeenStage = matches[1]
				cur = t
			}

		} else if err := json.Unmarshal([]byte(line), &info); err == nil {
			if cur != nil && (info.Suite == "" || info.Test == "") {
				cur.Output = appenLineWithTimeStamp(cur.Output, info)
			} else if testmap, mapok := suites[info.Suite]; mapok {
				if test, ok := testmap[info.Test]; ok {
					test.Output = appenLineWithTimeStamp(test.Output, info)
				} else {
					t := &Test{
						Name: info.Test,
					}
					t.Output = appenLineWithTimeStamp(t.Output, info)
					testmap[info.Test] = t
				}
			} else {
				t := &Test{
					Name: info.Test,
				}
				t.Output = appenLineWithTimeStamp(t.Output, info)
				m := make(map[string]*Test)
				m[info.Test] = t
				suites[info.Suite] = m
			}
		} else if cur != nil {
			if cur.Result == FAIL {
				cur.Failure = append(cur.Failure, line)
			} else if cur.Result == SKIP {
				cur.SkipMsg = append(cur.SkipMsg, line)
			}
		}
	}

	var finalTests []*Test
	var suiteTime = time.Duration(0)
	for _, testinfo := range initSuite {
		finalTests = append(finalTests, testinfo)
		suiteTime += testinfo.Duration
	}
	report.Packages = append(report.Packages, Package{
		Name:     "InitializationSteps",
		Duration: suiteTime,
		Time:     int(suiteTime / time.Millisecond),
		Tests:    finalTests,
	})

	return report, nil
}

func appenLineWithTimeStamp(output []string, infoLine Info) []string {
	line := fmt.Sprintf("%s %s", infoLine.Time, infoLine.Msg)
	return append(output, line)
}

// parseLine parses the string
func parseLine(t string, suites map[string]map[string]*Test) map[string]map[string]*Test {
	if t == "" {
		return suites
	}

	var info Info
	err := json.Unmarshal([]byte(t), &info)
	if err == nil {
		if test, ok := suites[info.Suite]; !ok {
			m := make(map[string]*Test)
			t := &Test{
				Name: info.Test,
			}
			t.Output = append(t.Output, info.Msg)
			m[info.Test] = t
			suites[info.Suite] = m
		} else {
			if t, present := test[info.Test]; present {
				t.Output = append(t.Output, info.Msg)
			} else {
				t := &Test{
					Name: info.Test,
				}
				t.Output = append(t.Output, info.Msg)
				test[info.Test] = t
				suites[info.Suite] = test
			}
		}
		return suites

	} else {
		// Do nothing, random output which is not of json
		return suites
	}
}

func parseSeconds(t string) time.Duration {
	if t == "" {
		return time.Duration(0)
	}
	// ignore error
	d, _ := time.ParseDuration(t + "s")
	return d
}

func parseNanoseconds(t string) time.Duration {
	// note: if input < 1 ns precision, result will be 0s.
	if t == "" {
		return time.Duration(0)
	}
	// ignore error
	d, _ := time.ParseDuration(t + "ns")
	return d
}

func findTest(tests []*Test, name string) *Test {
	for i := len(tests) - 1; i >= 0; i-- {
		if tests[i].Name == name {
			return tests[i]
		}
	}
	return nil
}

// Failures counts the number of failed tests in this report
func (r *Report) Failures() int {
	count := 0

	for _, p := range r.Packages {
		for _, t := range p.Tests {
			if t.Result == FAIL {
				count++
			}
		}
	}

	return count
}
