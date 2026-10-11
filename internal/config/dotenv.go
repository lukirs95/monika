package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var envKey = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// parseDotEnv liest eine strikte Teilmenge des .env-Formats: KEY=VALUE je Zeile, Kommentare nur
// als ganze Zeile mit #, Werte optional in einfachen oder doppelten Anführungszeichen und dann
// wörtlich. Es gibt keine Ersetzung von ${VAR}, kein export, keine Mehrzeiler. So wird ein
// Master-Key (Base64 mit =, /, +) nie umgedeutet.
func parseDotEnv(r io.Reader) (map[string]string, error) {
	vars := map[string]string{}
	seen := map[string]bool{}
	var errs []error
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch {
		case !ok || !envKey.MatchString(key):
			errs = append(errs, fmt.Errorf("line %d: want KEY=VALUE with KEY in [A-Z0-9_]", n))
			continue
		case seen[key]:
			errs = append(errs, fmt.Errorf("line %d: %s set twice", n, key))
			continue
		}
		if q := value[:min(1, len(value))]; q == `"` || q == "'" {
			if len(value) < 2 || !strings.HasSuffix(value, q) {
				errs = append(errs, fmt.Errorf("line %d: unterminated quote", n))
				continue
			}
			value = value[1 : len(value)-1]
		}
		vars[key], seen[key] = value, true
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, err)
	}
	return vars, errors.Join(errs...)
}
