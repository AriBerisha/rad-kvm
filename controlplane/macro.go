package main

// Macro scripts: one step per line.
//   text:hello world          type a string as keystrokes
//   key:Enter                 press one key (JS KeyboardEvent.code)
//   chord:ControlLeft+AltLeft+Delete   press keys together, then release
//   delay:500                 wait N milliseconds
//   # comment / blank lines ignored

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type macroStep struct {
	kind  string // text | chord | delay
	text  string
	codes []string
	ms    int
}

func parseMacro(script string) ([]macroStep, error) {
	var steps []macroStep
	for i, line := range strings.Split(script, "\n") {
		ln := strings.TrimSpace(line)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		kv := strings.SplitN(ln, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("line %d: expected 'type:value'", i+1)
		}
		kind, val := strings.TrimSpace(kv[0]), kv[1]
		switch kind {
		case "text":
			steps = append(steps, macroStep{kind: "text", text: val})
		case "key":
			steps = append(steps, macroStep{kind: "chord", codes: []string{strings.TrimSpace(val)}})
		case "chord":
			var codes []string
			for _, c := range strings.Split(val, "+") {
				if c = strings.TrimSpace(c); c != "" {
					codes = append(codes, c)
				}
			}
			if len(codes) == 0 {
				return nil, fmt.Errorf("line %d: empty chord", i+1)
			}
			steps = append(steps, macroStep{kind: "chord", codes: codes})
		case "delay":
			ms, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || ms < 0 {
				return nil, fmt.Errorf("line %d: bad delay (want a non-negative integer)", i+1)
			}
			steps = append(steps, macroStep{kind: "delay", ms: ms})
		default:
			return nil, fmt.Errorf("line %d: unknown step %q", i+1, kind)
		}
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("macro is empty")
	}
	return steps, nil
}

func (h *HID) RunMacro(steps []macroStep) {
	for _, s := range steps {
		switch s.kind {
		case "text":
			h.Type(s.text)
		case "chord":
			h.Chord(s.codes)
		case "delay":
			time.Sleep(time.Duration(s.ms) * time.Millisecond)
		}
	}
}
