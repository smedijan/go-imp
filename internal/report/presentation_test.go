package report_test

import (
	"bytes"
	"fmt"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
	"github.com/smedijan/go-imp/internal/report/internal"
)

func TestFindingPresentationMatrix(t *testing.T) {
	statuses := []mutator.Status{mutator.Lived, mutator.NotCovered, mutator.TimedOut, mutator.Runnable,
		mutator.Killed, mutator.Skipped, mutator.NotViable}
	for _, status := range statuses {
		for _, verbose := range []bool{false, true} {
			for _, withCommand := range []bool{false, true} {
				for _, snippet := range []struct {
					name      string
					original  string
					mutated   string
					different bool
				}{
					{name: "missing"},
					{name: "original only", original: "return a > b\n"},
					{name: "mutated only", mutated: "return a >= b\n"},
					{name: "identical", original: "return a > b\n", mutated: "return a > b\n"},
					{name: "changed", original: "return a > b\n", mutated: "return a >= b\n", different: true},
				} {
					name := fmt.Sprintf("%s/verbose=%t/command=%t/%s", status, verbose, withCommand, snippet.name)
					t.Run(name, func(t *testing.T) {
						m := stubMutant{status: status, mutantType: mutator.ConditionalsBoundary,
							position: token.Position{Filename: "case.go", Line: 7, Column: 3},
							original: []byte(snippet.original), mutated: []byte(snippet.mutated)}
						if withCommand {
							m.command = []string{"go", "test", "example.com/pkg"}
						}
						var out bytes.Buffer
						if err := report.Write(&out, report.Results{Mutants: []mutator.Mutator{m}, Elapsed: 1250 * time.Millisecond}, report.Options{Verbose: verbose}); err != nil {
							t.Fatal(err)
						}
						text := out.String()
						for _, expected := range []string{"completed in 1 second 250 milliseconds\n", status.String() + " (1) - ", "  case.go\n", "    7:3 CONDITIONALS_BOUNDARY\n"} {
							if strings.Count(text, expected) != 1 {
								t.Errorf("expected exactly one %q in:\n%s", expected, text)
							}
						}
						wantDiff := snippet.different && (verbose || status == mutator.Lived || status == mutator.NotCovered)
						if got := strings.Contains(text, "-return a > b"); got != wantDiff {
							t.Errorf("diff visible=%t, want %t:\n%s", got, wantDiff, text)
						}
						wantCommand := withCommand && (verbose || status == mutator.Lived)
						if got := strings.Contains(text, "Test command:"); got != wantCommand {
							t.Errorf("command visible=%t, want %t:\n%s", got, wantCommand, text)
						}
						if got := strings.Contains(text, "Survivors require judgment:"); got != (status == mutator.Lived) {
							t.Errorf("survivor warning visible=%t for %s", got, status)
						}
						if strings.Contains(text, "No findings") {
							t.Error("nonempty selected group was reported as empty")
						}
					})
				}
			}
		}
	}
}

func TestExactFindingOrder(t *testing.T) {
	// Exercise all four tie-breakers, including equal source coordinates.
	ordered := []stubMutant{
		{position: token.Position{Filename: "a.go", Line: 2, Column: 3}, mutantType: mutator.ConditionalsBoundary, status: mutator.Lived},
		{position: token.Position{Filename: "a.go", Line: 2, Column: 3}, mutantType: mutator.ConditionalsNegation, status: mutator.Lived},
		{position: token.Position{Filename: "a.go", Line: 2, Column: 4}, mutantType: mutator.ArithmeticBase, status: mutator.Lived},
		{position: token.Position{Filename: "a.go", Line: 3, Column: 1}, mutantType: mutator.ArithmeticBase, status: mutator.Lived},
		{position: token.Position{Filename: "z.go", Line: 1, Column: 1}, mutantType: mutator.ArithmeticBase, status: mutator.Lived},
	}
	for _, permutation := range [][]int{{0, 1, 2, 3, 4}, {4, 3, 2, 1, 0}, {2, 4, 0, 3, 1}, {1, 0, 3, 4, 2}} {
		t.Run(fmt.Sprint(permutation), func(t *testing.T) {
			var mutants []mutator.Mutator
			for _, i := range permutation {
				mutants = append(mutants, ordered[i])
			}
			results := report.Results{Mutants: mutants}
			document, _ := renderJSON(t, results, report.Options{})
			want := []internal.Mutation{
				{Line: 2, Column: 3, Type: "CONDITIONALS_BOUNDARY"},
				{Line: 2, Column: 3, Type: "CONDITIONALS_NEGATION"},
				{Line: 2, Column: 4, Type: "ARITHMETIC_BASE"},
				{Line: 3, Column: 1, Type: "ARITHMETIC_BASE"},
			}
			if diff := cmp.Diff(want, document.Groups["LIVED"]["a.go"]); diff != "" {
				t.Error(diff)
			}
			var text bytes.Buffer
			if err := report.Write(&text, results, report.Options{}); err != nil {
				t.Fatal(err)
			}
			if strings.Count(text.String(), "  a.go\n") != 1 || strings.Count(text.String(), "  z.go\n") != 1 {
				t.Error("files must each have one group heading")
			}
			previous := -1
			for _, fragment := range []string{"  a.go\n", "    2:3 CONDITIONALS_BOUNDARY", "    2:3 CONDITIONALS_NEGATION", "    2:4 ARITHMETIC_BASE", "    3:1 ARITHMETIC_BASE", "  z.go\n"} {
				index := strings.Index(text.String(), fragment)
				if index <= previous {
					t.Errorf("out-of-order or missing %q in:\n%s", fragment, &text)
				}
				previous = index
			}
		})
	}
}

func TestFilteredTextEmptyStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutants  []mutator.Mutator
		statuses string
		empty    bool
		warning  bool
	}{
		{name: "no mutants", empty: true},
		{name: "survivors filtered out", mutants: []mutator.Mutator{stubMutant{status: mutator.Lived}}, statuses: "killed", empty: true},
		{name: "no survivors", mutants: []mutator.Mutator{stubMutant{status: mutator.Killed}}},
		{name: "only survivors", mutants: []mutator.Mutator{stubMutant{status: mutator.Lived}}, warning: true},
		{name: "survivors selected but absent", mutants: []mutator.Mutator{stubMutant{status: mutator.NotCovered}}, statuses: "lived", empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statuses, err := report.ParseStatuses(tc.statuses)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := report.Write(&out, report.Results{Mutants: tc.mutants}, report.Options{Statuses: statuses}); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(out.String(), "No findings for the selected statuses."); got != tc.empty {
				t.Errorf("empty message=%t, want %t", got, tc.empty)
			}
			if got := strings.Contains(out.String(), "Survivors require judgment:"); got != tc.warning {
				t.Errorf("warning=%t, want %t", got, tc.warning)
			}
		})
	}
}

func TestMutatorStatisticsCountAllInputs(t *testing.T) {
	for _, selected := range []string{"", "lived", "killed"} {
		t.Run(selected, func(t *testing.T) {
			statuses, err := report.ParseStatuses(selected)
			if err != nil {
				t.Fatal(err)
			}
			results := report.Results{Mutants: []mutator.Mutator{
				stubMutant{status: mutator.Lived, mutantType: mutator.ArithmeticBase},
				stubMutant{status: mutator.Killed, mutantType: mutator.ArithmeticBase},
				stubMutant{status: mutator.NotCovered, mutantType: mutator.ConditionalsBoundary},
				stubMutant{status: mutator.Skipped, mutantType: mutator.InvertLogical},
			}}
			doc, _ := renderJSON(t, results, report.Options{Statuses: statuses})
			want := map[string]int{"arithmetic_base": 2, "conditionals_boundary": 1, "invert_logical": 1}
			if diff := cmp.Diff(want, doc.MutatorStatistics); diff != "" {
				t.Error(diff)
			}
		})
	}
}
