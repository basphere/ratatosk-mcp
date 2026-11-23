package tools

// Arguments a tool no longer lists that a call may still send: accepted, and
// read as if they were not there.
//
// Why: every input schema forbids keys it does not list (additionalProperties
// false, as the SDK infers it), and the SDK checks a call against the schema
// before the handler runs, so a key taken out of a schema turns every call
// that still sends it into an error. lang left the tools in 1.3.0 (the
// answers are English alone, answerLang); a client written against 1.2.0
// sends it, and failing its call over a language preference would lose an
// answer whose content is the same in English (the owner, 2026-11-23). The
// schema stays strict for every other key: a misspelt argument is still
// refused rather than read as absent.

import (
	"context"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RetiredArguments are, by tool, the keys a tools/call may carry that the tool
// no longer reads: only the tools that took them. list_projects never took
// lang, and a call that sends it is refused as 1.2.0 refused it (review,
// 2026-11-23).
var RetiredArguments = map[string][]string{
	"check_stack": {"lang"}, "check_upgrade": {"lang"}, "get_release": {"lang"}, "list_releases": {"lang"}, "search": {"lang"},
}

// IgnoreRetiredArguments is a receiving middleware: it takes the called tool's
// RetiredArguments off a tools/call's arguments before the call is checked
// against the tool's schema. The request it passes on is a copy; the one the
// middleware around it holds keeps what the client sent.
func IgnoreRetiredArguments(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if method != "tools/call" || !ok || call.Params == nil || len(call.Params.Arguments) == 0 {
			return next(ctx, method, req)
		}
		retired := RetiredArguments[call.Params.Name]
		if len(retired) == 0 {
			return next(ctx, method, req)
		}
		fields, err := readObject(call.Params.Arguments)
		if err != nil {
			return next(ctx, method, req) // not an object: the SDK answers it as before
		}
		kept := slices.DeleteFunc(slices.Clone(fields), func(f objField) bool {
			return slices.Contains(retired, f.Key)
		})
		if len(kept) == len(fields) {
			return next(ctx, method, req)
		}
		params := *call.Params
		params.Arguments = writeObject(kept)
		c := *call
		c.Params = &params
		return next(ctx, method, &c)
	}
}
