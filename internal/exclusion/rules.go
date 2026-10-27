// Package exclusion provides file exclusion rules based on regex patterns.
package exclusion

import (
	"fmt"
	"go/parser"
	"go/token"
	"regexp"
	"strings"

	"github.com/spf13/viper"

	"github.com/smedijan/go-imp/internal/configuration"
)

// Rules contains explicit file include and exclude patterns.
type Rules struct {
	include []*regexp.Regexp
	exclude []*regexp.Regexp
}

// New creates file rules from the configuration.
func New() (Rules, error) {
	excludes, err := compileRules(viper.GetStringSlice(configuration.RunExcludeFiles), "exclude-files")
	if err != nil {
		return Rules{}, err
	}
	includes, err := compileRules(viper.GetStringSlice(configuration.RunIncludeFiles), "include-files")
	if err != nil {
		return Rules{}, err
	}

	return Rules{include: includes, exclude: excludes}, nil
}

func compileRules(values []string, name string) ([]*regexp.Regexp, error) {
	rules := make([]*regexp.Regexp, 0, len(values))
	for i, value := range values {
		rule, err := regexp.Compile(value)
		if err != nil {
			return nil, fmt.Errorf("error in %s param value #%d: %w", name, i, err)
		}
		rules = append(rules, rule)
	}

	return rules, nil
}

// IsFileExcluded returns true for explicitly excluded files, or generated files
// unless explicitly included. Explicit exclusions take precedence over includes.
func (r Rules) IsFileExcluded(path string, source []byte) bool {
	if matchesAny(r.exclude, path) {
		return true
	}
	if matchesAny(r.include, path) {
		return false
	}

	return IsGenerated(source)
}

func matchesAny(rules []*regexp.Regexp, path string) bool {
	for _, rule := range rules {
		if rule.MatchString(path) {
			return true
		}
	}

	return false
}

// IsGenerated reports whether source has Go's standard generated-code marker
// before the package clause.
func IsGenerated(source []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), "generated.go", source, parser.ParseComments)
	if err != nil {
		return false
	}
	marker := regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			continue
		}
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//") && marker.MatchString(comment.Text) {
				return true
			}
		}
	}

	return false
}
