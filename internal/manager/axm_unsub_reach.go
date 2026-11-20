package manager

import (
	"text/template/parse"

	"github.com/knadh/listmonk/models"
)

// UnsubscribeReach reads the campaign's mail as it is put together — the base template (the
// campaign's template, or just the body for a visual campaign or none), every template it calls,
// the body (the "content" template) and the alternative body — on Go's own parse trees, and
// tells whether some place that prints UnsubscribeURL is reached on every path: not under an
// if or with that reads the subscriber, a variable or the clock, and not in a range (which can
// run no times), down every {{ template }} call that reaches it. found is false when nothing
// prints it or the sources cannot be parsed.
//
// Rendering the mail for a few subscribers cannot show that every recipient gets the link; this
// reads where the link is (LST-48 reviews: the link only in the full edition, the body included
// only for it, and the body included from a named template called only for it).
func (m *Manager) UnsubscribeReach(c *models.Campaign) (unconditional, found bool) {
	base := c.TemplateBody
	if base == "" || c.ContentType == models.CampaignContentTypeVisual {
		base = `{{ template "content" . }}`
	}
	alt := ""
	if c.AltBody.Valid {
		alt = c.AltBody.String
	}
	return unsubscribeReach(base, c.Body, alt, m.TemplateFuncs(c))
}

func unsubscribeReach(base, body, alt string, funcs map[string]any) (unconditional, found bool) {
	trees := map[string]*parse.Tree{}
	add := func(name, src string) bool {
		set, err := parse.Parse(name, src, "", "", funcs, builtins)
		if err != nil {
			return false
		}
		for k, t := range set {
			// The body's definitions do not replace the base's (as AddParseTree for "content").
			if _, ok := trees[k]; !ok || k == name {
				trees[k] = t
			}
		}
		return true
	}
	if !add(models.BaseTpl, base) || !add(models.ContentTpl, body) {
		return false, false
	}
	w := reachWalker{trees: trees}
	if t := trees[models.BaseTpl]; t != nil && t.Root != nil {
		w.walk(t.Root, false, 0)
	}
	if alt != "" {
		if set, err := parse.Parse("alt", alt, "", "", funcs, builtins); err == nil && set["alt"] != nil {
			aw := reachWalker{trees: set}
			aw.walk(set["alt"].Root, false, 0)
			w.found, w.free = w.found || aw.found, w.free || aw.free
		}
	}
	return w.free, w.found
}

// builtins are the functions text/template has besides the ones given. The parser only needs to
// know the names (with a value that is not nil).
var builtins = func() map[string]any {
	out := map[string]any{}
	for _, n := range []string{"and", "call", "html", "index", "slice", "js", "len", "not", "or", "print",
		"printf", "println", "urlquery", "eq", "ge", "gt", "le", "lt", "ne"} {
		out[n] = true
	}
	return out
}()

type reachWalker struct {
	trees       map[string]*parse.Tree
	found, free bool
	// holds is the variables that were given the unsubscribe address ($x := UnsubscribeURL .):
	// holdAlways when on every path, holdSometimes when only under a condition (or overwritten
	// under one).
	holds map[string]int
}

const (
	holdAlways = 1 + iota
	holdSometimes
)

const maxTemplateDepth = 20

func (w *reachWalker) walk(n parse.Node, cond bool, depth int) {
	if n == nil || depth > maxTemplateDepth {
		return
	}
	switch x := n.(type) {
	case *parse.ListNode:
		if x == nil {
			return
		}
		for _, c := range x.Nodes {
			w.walk(c, cond, depth)
		}
	case *parse.ActionNode:
		// An assignment prints nothing: it only gives the variable the address (LST-48 review:
		// {{ $leave := UnsubscribeURL . }} before a link printed only for the full edition).
		if len(x.Pipe.Decl) > 0 || x.Pipe.IsAssign {
			gives := w.givesAddress(x.Pipe) > 0
			whole := w.givesAddress(x.Pipe) == holdAlways
			if w.holds == nil {
				w.holds = map[string]int{}
			}
			for _, v := range x.Pipe.Decl {
				name, prev := v.Ident[0], w.holds[v.Ident[0]]
				switch {
				case gives && !cond && whole:
					w.holds[name] = holdAlways
				case gives && prev == holdAlways && whole:
					// The address either way.
				case gives:
					w.holds[name] = holdSometimes
				case !cond:
					delete(w.holds, name)
				case prev > 0:
					// Overwritten on some path only.
					w.holds[name] = holdSometimes
				}
			}
			return
		}
		if g := w.givesAddress(x.Pipe); g > 0 {
			w.found = true
			w.free = w.free || (!cond && g == holdAlways)
		}
	case *parse.IfNode:
		c := cond || dependent(x.Pipe)
		w.walk(x.List, c, depth)
		w.walk(x.ElseList, c, depth)
	case *parse.WithNode:
		c := cond || dependent(x.Pipe)
		w.walk(x.List, c, depth)
		w.walk(x.ElseList, c, depth)
	case *parse.RangeNode:
		// A range can run no times.
		w.walk(x.List, true, depth)
		w.walk(x.ElseList, true, depth)
	case *parse.TemplateNode:
		t := w.trees[x.Name]
		if t == nil || t.Root == nil {
			return
		}
		// Called with another dot than ".": the fields it reads may be the subscriber's.
		c := cond || (x.Pipe != nil && !isDot(x.Pipe))
		w.walk(t.Root, c, depth+1)
	}
}

// dependent tells whether a condition can differ from mail to mail: it reads the subscriber, a
// variable (which may hold anything), or the time (Date, now, the campaign's times).
func dependent(p *parse.PipeNode) bool {
	dep := false
	inspect(p, func(n parse.Node) {
		switch x := n.(type) {
		case *parse.VariableNode:
			dep = true
		case *parse.FieldNode:
			for _, id := range x.Ident {
				if id == "Subscriber" || id == "UpdatedAt" || id == "StartedAt" || id == "Sent" || id == "ToSend" {
					dep = true
				}
			}
		case *parse.ChainNode:
			for _, id := range x.Field {
				if id == "Subscriber" || id == "UpdatedAt" || id == "StartedAt" {
					dep = true
				}
			}
		case *parse.IdentifierNode:
			if x.Ident == "Date" || x.Ident == "now" {
				dep = true
			}
		}
	})
	return dep
}

// givesAddress tells whether a pipeline's value is the unsubscribe address itself: its last
// command is UnsubscribeURL (holdAlways), or a variable that was given it (that variable's
// state). Anything else (another function around it, a field of it) gives 0: the check then
// warns rather than passes.
func (w *reachWalker) givesAddress(p *parse.PipeNode) int {
	if p == nil || len(p.Cmds) == 0 {
		return 0
	}
	last := p.Cmds[len(p.Cmds)-1]
	if len(last.Args) == 0 {
		return 0
	}
	switch a := last.Args[0].(type) {
	case *parse.IdentifierNode:
		if a.Ident == "UnsubscribeURL" {
			return holdAlways
		}
	case *parse.VariableNode:
		if len(a.Ident) == 1 && len(last.Args) == 1 {
			return w.holds[a.Ident[0]]
		}
	}
	return 0
}

func isDot(p *parse.PipeNode) bool {
	if len(p.Decl) > 0 || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	_, ok := p.Cmds[0].Args[0].(*parse.DotNode)
	return ok
}

// inspect visits the nodes of a pipeline and its commands' arguments.
func inspect(n parse.Node, f func(parse.Node)) {
	if n == nil {
		return
	}
	f(n)
	switch x := n.(type) {
	case *parse.PipeNode:
		if x == nil {
			return
		}
		for _, v := range x.Decl {
			inspect(v, f)
		}
		for _, c := range x.Cmds {
			inspect(c, f)
		}
	case *parse.CommandNode:
		for _, a := range x.Args {
			inspect(a, f)
		}
	case *parse.ChainNode:
		inspect(x.Node, f)
	}
}
