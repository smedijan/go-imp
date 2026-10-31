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

// Package workdir manages temporary working directories for isolated mutation testing.
package workdir

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/smedijan/go-imp/internal/log"
)

// Dealer is the responsible for creating and returning the reference
// to a workdir to use during mutation testing instead of the actual
// source code.
//
// It has two methods:
//
//		Get that returns a folder name that will be used by imp as workdir.
//	    Clean that must be called to remove all the created folders.
type Dealer interface {
	Get(idf string) (string, error)
	Clean()
	WorkDir() string
}

// CachedDealer is the implementation of the Dealer interface, responsible
// for creating a working directory of a source directory. It allows
// imp not to work in the actual source directory messing up
// with the source code files.
type CachedDealer struct {
	mutex      *sync.RWMutex
	cache      map[string]string
	workDir    string
	srcDir     string
	excludes   []string
	excludeGit bool
}

// NewCachedDealer instantiates a new Dealer that keeps a cache of the
// instantiated folders. Every time a new working directory is requested
// with the same identifier, the same folder reference is returned. Exclusions
// are exact module-relative paths; excluded directories are not traversed.
func NewCachedDealer(workDir, srcDir string, excludes ...string) *CachedDealer {
	dealer := &CachedDealer{
		mutex:    &sync.RWMutex{},
		cache:    make(map[string]string),
		workDir:  workDir,
		srcDir:   srcDir,
		excludes: make([]string, 0, len(excludes)),
	}
	for _, exclude := range excludes {
		exclude = filepath.Clean(filepath.FromSlash(exclude))
		if exclude != "." && !filepath.IsAbs(exclude) && exclude != ".." && !strings.HasPrefix(exclude, ".."+string(filepath.Separator)) {
			dealer.excludes = append(dealer.excludes, exclude)
		}
	}

	return dealer
}

// Snapshot captures source before workers run, excluding Git metadata (including
// worktree pointer files). The caller owns removal of the returned directory.
func (cd *CachedDealer) Snapshot(ctx context.Context) (string, error) {
	snapshot := NewCachedDealer(cd.workDir, cd.srcDir, cd.excludes...)
	snapshot.excludeGit = true

	return snapshot.GetContext(ctx, "pristine")
}

// Get provides a working directory where all the files are full copies
// to the original files in the source directory.
func (cd *CachedDealer) Get(idf string) (string, error) {
	return cd.GetContext(context.Background(), idf)
}

// GetContext is Get with cancellation checks during source traversal/copying.
func (cd *CachedDealer) GetContext(ctx context.Context, idf string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	dstDir, ok := cd.fromCache(idf)
	if ok {
		return dstDir, nil
	}

	dstDir, err := os.MkdirTemp(cd.workDir, "wd-*")
	if err != nil {
		return "", err
	}
	err = filepath.Walk(cd.srcDir, cd.copyTo(ctx, dstDir))
	if err != nil {
		if cleanupErr := os.RemoveAll(dstDir); cleanupErr != nil {
			return "", fmt.Errorf("copy source: %w (remove partial workdir: %w)", err, cleanupErr)
		}

		return "", err
	}

	cd.setCache(idf, dstDir)

	return dstDir, nil
}

// WorkDir provides the root working directory.
func (cd *CachedDealer) WorkDir() string {
	return cd.workDir
}

// Clean frees all the cached folders and removes all of them from disk.
func (cd *CachedDealer) Clean() {
	for _, v := range cd.cache {
		err := os.RemoveAll(v)
		if err != nil {
			log.Errorf("impossible to remove temporary folder %s: %s\n", v, err)
		}
	}
	cd.cache = make(map[string]string)
}

func (cd *CachedDealer) fromCache(idf string) (string, bool) {
	cd.mutex.RLock()
	defer cd.mutex.RUnlock()
	dstDir, ok := cd.cache[idf]
	if ok {
		return dstDir, true
	}

	return "", false
}

func (cd *CachedDealer) setCache(idf, folder string) {
	cd.mutex.Lock()
	defer cd.mutex.Unlock()
	cd.cache[idf] = folder
}

func (cd *CachedDealer) copyTo(ctx context.Context, dstDir string) func(srcPath string, info fs.FileInfo, err error) error {
	return func(srcPath string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relPath, err := filepath.Rel(cd.srcDir, srcPath)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}
		if cd.excludeGit && info.Name() == ".git" {
			if info.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}
		for _, exclude := range cd.excludes {
			if relPath == exclude {
				if info.IsDir() {
					return filepath.SkipDir
				}

				return nil
			}
		}
		dstPath := filepath.Join(dstDir, relPath)

		return copyPath(srcPath, dstPath, info)
	}
}

func copyPath(srcPath, dstPath string, info fs.FileInfo) error {
	switch mode := info.Mode(); {
	case mode.IsDir():
		if err := os.Mkdir(dstPath, mode); err != nil && !os.IsExist(err) {
			return err
		}
	case mode.IsRegular():
		if err := doCopy(srcPath, dstPath, mode); err != nil {
			return err
		}
	}

	return nil
}

func doCopy(srcPath, dstPath string, fileMode fs.FileMode) error {
	// #nosec G304 -- srcPath is produced by walking the module tree to copy its files.
	s, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer s.Close()
	// #nosec G304 -- dstPath is the corresponding path in the isolated worker directory.
	d, err := os.OpenFile(dstPath, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return err
	}

	if _, err = io.Copy(d, s); err != nil {
		_ = d.Close()

		return err
	}

	return d.Close()
}
