// Package config loads local credentials.
package config

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// LoadDotEnv reads key=value pairs from path and applies them to the process
// environment, returning the names it set. A missing file is not an error.
//
// Values in the file override anything already in the environment. That is
// deliberate: a .env sitting next to the project is an explicit, visible choice,
// while an inherited variable may be a stale leftover from some other tool --
// and silently preferring the inherited one is how you end up billing an
// account you forgot you had.
func LoadDotEnv(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return names, err
		}
		names = append(names, key)
	}
	if err := sc.Err(); err != nil {
		return names, err
	}
	sort.Strings(names)
	return names, nil
}

func parseLine(line string) (key, val string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")

	key, val, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}

	val = strings.TrimSpace(val)
	// Strip one matching pair of surrounding quotes, if present.
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
	}
	return key, val, true
}

// Fingerprint identifies a secret without disclosing it, so logs and reports
// can say which credential was used.
func Fingerprint(secret string) string {
	if secret == "" {
		return "(unset)"
	}
	if len(secret) <= 6 {
		return "(too short to fingerprint)"
	}
	return secret[:6] + "..." + " (len " + itoa(len(secret)) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
