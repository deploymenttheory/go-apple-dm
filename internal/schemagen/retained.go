package schemagen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// currentSubmodule names the .gitmodules entry for Apple's current schema checkout.
const currentSubmodule = "apple-device-management-current"

// ErrRetainedRelease reports a submodule beside the current checkout that is
// not named for the schema release it retains.
var ErrRetainedRelease = errors.New("schemagen: retained release")

// releaseDirectory matches a retained checkout's directory name: the version of
// the schema release it pins, such as 26.4.
var releaseDirectory = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)

// RetainedReleases returns the checkouts of earlier major schema releases that
// generation retains for devices still running those operating systems, newest
// first. Each is a submodule beside Apple's current checkout, in a directory
// named for the final schema release Apple published for that major version,
// such as third_party/apple-device-management/26.4. Returned paths are relative
// to the directory containing gitmodules.
//
// A missing gitmodules file returns an error wrapping fs.ErrNotExist. A file
// without the current checkout's entry retains nothing. A sibling directory
// that is not a version fails with ErrRetainedRelease.
func RetainedReleases(gitmodules string) ([]string, error) {
	if _, err := os.Stat(gitmodules); err != nil {
		return nil, fmt.Errorf("schemagen: retained releases: %w", err)
	}
	out, err := exec.CommandContext(context.Background(),
		"git",
		"config",
		"--file",
		gitmodules,
		"--get-regexp",
		`^submodule\..*\.path$`,
	).Output() // #nosec G204 -- repository configuration path
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil // No submodules.
	}
	if err != nil {
		return nil, fmt.Errorf("schemagen: read %s: %w", gitmodules, err)
	}
	paths := map[string]string{}
	for line := range strings.Lines(string(out)) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		paths[strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")] = value
	}
	current, ok := paths[currentSubmodule]
	if !ok {
		return nil, nil
	}
	var releases []string
	for name, value := range paths {
		if name == currentSubmodule || path.Dir(value) != path.Dir(current) {
			continue
		}
		if !releaseDirectory.MatchString(path.Base(value)) {
			return nil, fmt.Errorf("%w: %s is not named for a schema release version", ErrRetainedRelease, value)
		}
		releases = append(releases, value)
	}
	sort.Slice(releases, func(i, j int) bool {
		return compareVersions(path.Base(releases[i]), path.Base(releases[j])) > 0
	})
	for i, value := range releases {
		releases[i] = filepath.Join(filepath.Dir(gitmodules), filepath.FromSlash(value))
	}
	return releases, nil
}
