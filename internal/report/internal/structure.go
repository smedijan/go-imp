/*
 * Copyright 2022 The Gremlins Authors
 *
 *    Licensed under the Apache License, Version 2.0 (the "License");
 *    you may not use this file except in compliance with the License.
 *    You may obtain a copy of the License at
 *
 *        http://www.apache.org/licenses/LICENSE-2.0
 *
 *    Unless required by applicable law or agreed to in writing, software
 *    distributed under the License is distributed on an "AS IS" BASIS,
 *    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *    See the License for the specific language governing permissions and
 *    limitations under the License.
 */

// Package internal provides data structures for the imp JSON output format.
package internal

// OutputResult is the data structure for the imp file output format.
type OutputResult struct {
	OutputSummary
	Files []OutputFile `json:"files"`
}

// OutputSummary contains metrics shared by the legacy and detailed JSON reports.
type OutputSummary struct {
	GoModule          string      `json:"go_module"`
	TestEfficacy      float64     `json:"test_efficacy"`
	MutationsCoverage float64     `json:"mutations_coverage"`
	MutantsTotal      int         `json:"mutants_total"`
	MutantsKilled     int         `json:"mutants_killed"`
	MutantsLived      int         `json:"mutants_lived"`
	MutantsNotViable  int         `json:"mutants_not_viable"`
	MutantsNotCovered int         `json:"mutants_not_covered"`
	ElapsedTime       float64     `json:"elapsed_time"`
	MutatorStatistics MutatorType `json:"mutator_statistics"`
}

// DetailedOutputResult groups findings by status, then source filename.
type DetailedOutputResult struct {
	OutputSummary
	MutantsTimedOut int                                      `json:"mutants_timed_out"`
	MutantsSkipped  int                                      `json:"mutants_skipped"`
	MutantsRunnable int                                      `json:"mutants_runnable"`
	Groups          map[string]map[string][]DetailedMutation `json:"groups"`
}

// DetailedMutation omits the status and filename supplied by its enclosing group.
type DetailedMutation struct {
	Type        string `json:"type"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	TestCommand string `json:"test_command,omitempty"`
}

// OutputFile represents a single file in the OutputResult data structure.
type OutputFile struct {
	Filename  string     `json:"file_name"`
	Mutations []Mutation `json:"mutations"`
}

// Mutation represents a single mutation in the OutputResult data structure.
type Mutation struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// MutatorType contains the list of all supported mutator types.
type MutatorType struct {
	ArithmeticBase           int `json:"arithmetic_base,omitempty"`
	ConditionalsNegation     int `json:"conditionals_negation,omitempty"`
	ConditionalsBoundary     int `json:"conditionals_boundary,omitempty"`
	IncrementDecrement       int `json:"increment_decrement,omitempty"`
	InvertAssignments        int `json:"invert_assignments,omitempty"`
	InvertBitwise            int `json:"invert_bitwise,omitempty"`
	InvertBitwiseAssignments int `json:"invert_bitwise_assignments,omitempty"`
	InvertLogical            int `json:"invert_logical,omitempty"`
	InvertLoopCtrl           int `json:"invert_loop_ctrl,omitempty"`
	InvertNegatives          int `json:"invert_negatives,omitempty"`
	RemoveSelfAssignments    int `json:"remove_self_assignments,omitempty"`
}
