package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/tui"
)

// runCall is --call <method> [params]: one call to the local daemon, which it starts when nothing
// answers, as --ui does. params is a JSON array (none: no parameters); the result is written to out
// as JSON. It is the client for a program with no msgpack-rpc of its own: an AI agent's shell, a
// script (AGENTS.md).
func runCall(ctx context.Context, configPath, method, params string, out io.Writer) error {
	if configPath == "" {
		var err error
		if configPath, err = config.DefaultPath(); err != nil {
			return err
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	stateDir, err := cfg.Server.StateDirPath()
	if err != nil {
		return err
	}
	sock, err := cfg.Server.SocketPath()
	if err != nil {
		return err
	}
	args, err := callParams(params)
	if err != nil {
		return err
	}
	session := tui.NewSession(sock, func() (string, error) { return tui.SpawnServe(configPath, stateDir) })
	if err := session.Connect(ctx); err != nil {
		return err
	}
	res, err := session.Call(ctx, method, args...)
	if err != nil {
		var re *golibrpc.Error
		if errors.As(err, &re) {
			return &CallError{Code: re.Code, Message: re.Message}
		}
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(jsonOf(res))
}

// callMain is --call's exit code: 0 with the result on stdout; 1 with the daemon's refusal as
// {"error": {code, message}} on stderr, or any other failure as text.
func callMain(ctx context.Context, configPath, method, params string, stdout, stderr io.Writer) int {
	err := runCall(ctx, configPath, method, params, stdout)
	if err == nil {
		return 0
	}
	var ce *CallError
	if errors.As(err, &ce) {
		b, _ := json.Marshal(map[string]any{"error": ce})
		fmt.Fprintln(stderr, string(b))
	} else {
		fmt.Fprintln(stderr, "autodoc:", err)
	}
	return 1
}

// CallError is the daemon refusing a call: its code and message, as --call prints them.
type CallError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

func (e *CallError) Error() string { return fmt.Sprintf("%s (code %d)", e.Message, e.Code) }

// callParams reads --call's parameters: a JSON array, its integers as integers (the verbs take
// int64s), its other numbers as float64s.
func callParams(s string) ([]any, error) {
	if s == "" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("--call: the parameters are not JSON: %w", err)
	}
	if dec.More() {
		return nil, errors.New("--call: the parameters are one JSON array, with nothing after it")
	}
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New(`--call: the parameters are a JSON array, as ["kb", "a query"]`)
	}
	return fromJSON(list).([]any), nil
}

func fromJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	case []any:
		for i := range x {
			x[i] = fromJSON(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = fromJSON(x[k])
		}
		return x
	}
	return v
}

// jsonOf is a result as JSON shows it: bytes (a file's content) as text when they are UTF-8.
func jsonOf(v any) any {
	switch x := v.(type) {
	case []byte:
		if utf8.Valid(x) {
			return string(x)
		}
		return x
	case []any:
		for i := range x {
			x[i] = jsonOf(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = jsonOf(x[k])
		}
		return x
	}
	return v
}
