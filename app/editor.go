package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// runEditor is --editor <verb> [params]: one call to the page of the TUI that started this agent
// (ADR 1792014854), at $AUTODOC_EDITOR, with the secret it was started with, $AUTODOC_EDITOR_TOKEN,
// which the TUI knows it by. params is a JSON array, as --call's; the token goes first, and the
// answer is written to out as JSON.
func runEditor(ctx context.Context, verb, params string, env func(string) string, out io.Writer) error {
	sock, token := env("AUTODOC_EDITOR"), env("AUTODOC_EDITOR_TOKEN")
	if sock == "" || token == "" {
		return errors.New("--editor reaches the page of the AutoDoc that started this agent: $AUTODOC_EDITOR and $AUTODOC_EDITOR_TOKEN are not set")
	}
	args, err := callParams(params)
	if err != nil {
		return err
	}
	cli, err := golibrpc.Dial(ctx, sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		return fmt.Errorf("--editor: the editor at %s does not answer: %w", sock, err)
	}
	defer cli.Close()
	res, err := cli.Call(ctx, verb, append([]any{token}, args...)...)
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

// editorMain is --editor's exit code, as --call's: 0 with the answer on stdout; 1 with a refusal as
// {"error": {code, message}} on stderr, or any other failure as text.
func editorMain(ctx context.Context, verb, params string, stdout, stderr io.Writer) int {
	err := runEditor(ctx, verb, params, os.Getenv, stdout)
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
