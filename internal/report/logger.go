// Package report formats and outputs mutation testing results.
package report

import (
	"errors"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/log"
	"github.com/smedijan/go-imp/internal/mutator"
)

// Filter maps mutation statuses to filter which mutants are logged.
type Filter = map[mutator.Status]struct{}

// ErrInvalidFilter is returned when an invalid status filter string is provided.
var ErrInvalidFilter = errors.New("invalid statuses filter, only 'lctkvsr' letters allowed")

// ErrInvalidDiffFilter is returned when an invalid diff status filter string is provided.
var ErrInvalidDiffFilter = errors.New("invalid statuses diff, only 'lk' letters allowed")

// MutantLogger prints mutant statuses based on filter and verbosity flags.
type MutantLogger struct {
	Filter
	DiffFilter  Filter
	SummaryOnly bool
}

// NewLogger creates a new MutantLogger with filters from configuration.
func NewLogger() MutantLogger {
	outputStatuses := configuration.Get[string](configuration.RunOutputStatusesKey)
	f, err := ParseFilter(outputStatuses)
	if err != nil {
		log.Infof("output-statuses filter not applied: %s\n", err)
	}

	diffStatuses := configuration.Get[string](configuration.RunOutputDiffStatusesKey)
	df, err := ParseDiffFilter(diffStatuses)
	if err != nil {
		log.Infof("output-diff-statuses filter not applied: %s\n", err)
	}

	return MutantLogger{
		Filter:      f,
		DiffFilter:  df,
		SummaryOnly: configuration.ExperimentalEnabled(configuration.ExperimentalDetailedReport) && outputStatuses == "" && diffStatuses == "",
	}
}

// Mutant logs a mutant if it passes the filter, then optionally prints the diff.
func (l MutantLogger) Mutant(m mutator.Mutator) {
	if l.SummaryOnly {
		return
	}
	if l.Filter != nil {
		if _, ok := l.Filter[m.Status()]; !ok {
			return
		}
	}

	Mutant(m)

	if _, ok := l.DiffFilter[m.Status()]; ok {
		MutantDiff(m)
	}
}

// ParseFilter parses a status filter string into a Filter map.
// Valid characters are 'lctkvsr' representing different mutation statuses.
func ParseFilter(s string) (Filter, error) {
	if s == "" {
		return nil, nil
	}

	result := Filter{}

	for _, r := range s {
		switch r {
		case 'l':
			result[mutator.Lived] = struct{}{}
		case 'c':
			result[mutator.NotCovered] = struct{}{}
		case 't':
			result[mutator.TimedOut] = struct{}{}
		case 'k':
			result[mutator.Killed] = struct{}{}
		case 'v':
			result[mutator.NotViable] = struct{}{}
		case 's':
			result[mutator.Skipped] = struct{}{}
		case 'r':
			result[mutator.Runnable] = struct{}{}
		default:
			return nil, ErrInvalidFilter
		}
	}

	return result, nil
}

// ParseDiffFilter parses a diff status filter string into a Filter map.
// Valid characters are 'l' and 'k' representing Lived and Killed statuses.
func ParseDiffFilter(s string) (Filter, error) {
	if s == "" {
		return nil, nil
	}

	result := Filter{}

	for _, r := range s {
		switch r {
		case 'l':
			result[mutator.Lived] = struct{}{}
		case 'k':
			result[mutator.Killed] = struct{}{}
		default:
			return nil, ErrInvalidDiffFilter
		}
	}

	return result, nil
}
