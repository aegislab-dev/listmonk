package manager

import (
	"html/template"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/knadh/listmonk/models"
)

// UnsubscribeOnEveryPath tells whether every recipient's mail has the unsubscribe link, as far
// as the campaign itself decides (LST-48 reviews). It compiles the campaign as the sender does,
// takes out of its templates (base, every defined one, the body, the alternative body) every
// part that can differ from one mail to the next — an if or with that reads the subscriber, a
// variable or the time, every range (it can run no times), a template called with another value
// than "." — and renders what is left for the given status: the conditions that are the same for
// every mail (a campaign setting, the status) take the branch they really take. If the link is
// still in the mail, it is in everyone's. A part taken out that assigns to a variable of the
// rest could change what is left: then nothing is vouched for.
//
// Reading the templates without running them took both branches of such a constant if as
// possible (a footer switched off by a campaign setting passed); running them does not.
func (m *Manager) UnsubscribeOnEveryPath(c *models.Campaign, sub models.Subscriber, status string) (bool, error) {
	msg, err := m.prepareCheck(c, sub, status, nil, true)
	if err != nil {
		return false, err
	}
	// A function that changes data in place (set on a dict, say) can carry a value of one
	// recipient out of any part, taken out or kept, into what is printed later: then nothing is
	// vouched for (LST-48 review: a footer map whose url a with-block set from a subscriber
	// attribute).
	for _, t := range msg.Campaign.Tpl.Templates() {
		if t.Tree != nil && callsMutator(t.Tree.Root) {
			return false, nil
		}
	}
	if msg.Campaign.AltBodyTpl != nil {
		for _, t := range msg.Campaign.AltBodyTpl.Templates() {
			if t.Tree != nil && callsMutator(t.Tree.Root) {
				return false, nil
			}
		}
	}
	// The rendered subject (.Subject) differs only when the subject template does.
	subj := m.subjectVaries(c, true)
	for _, t := range msg.Campaign.Tpl.Templates() {
		if t.Tree != nil && !pruneVarying(t.Tree.Root, map[string]bool{}, subj) {
			return false, nil
		}
	}
	if msg.Campaign.AltBodyTpl != nil {
		for _, t := range msg.Campaign.AltBodyTpl.Templates() {
			if t.Tree != nil && !pruneVarying(t.Tree.Root, map[string]bool{}, subj) {
				return false, nil
			}
		}
	}
	if err := msg.render(); err != nil {
		return false, err
	}
	return hasUnsubscribeLink(msg), nil
}

// pruneVarying takes out of a list the parts that can differ between mails (see
// UnsubscribeOnEveryPath), in place: also what prints a value that reads the subscriber or the
// time, or a variable given such a value (LST-48 review: {{ or .Subscriber.Attribs.footer_url
// (UnsubscribeURL .) }} printed the address for the example and another one for some people).
// varying is the variables of this template given such a value. false: a part taken out
// assigns to a variable.
func pruneVarying(l *parse.ListNode, varying map[string]bool, subj bool) bool {
	if l == nil {
		return true
	}
	keep := l.Nodes[:0]
	for _, n := range l.Nodes {
		drop := false
		switch x := n.(type) {
		case *parse.ActionNode:
			if valueVaries(x.Pipe, varying, subj) {
				if len(x.Pipe.Decl) > 0 {
					// A declaration prints nothing: it is kept, and its variables vary.
					for _, v := range x.Pipe.Decl {
						varying[v.Ident[0]] = true
					}
				} else {
					drop = true
				}
			} else if len(x.Pipe.Decl) > 0 && !x.Pipe.IsAssign {
				for _, v := range x.Pipe.Decl {
					delete(varying, v.Ident[0])
				}
			}
		case *parse.IfNode:
			if dependent(x.Pipe, subj) {
				drop = true
			} else if !pruneVarying(x.List, varying, subj) || !pruneVarying(x.ElseList, varying, subj) {
				return false
			}
		case *parse.WithNode:
			if dependent(x.Pipe, subj) {
				drop = true
			} else if !pruneVarying(x.List, varying, subj) || !pruneVarying(x.ElseList, varying, subj) {
				return false
			}
		case *parse.RangeNode:
			drop = true
		case *parse.TemplateNode:
			drop = x.Pipe != nil && !isDot(x.Pipe)
		}
		if drop {
			if assigns(n) {
				return false
			}
			continue
		}
		keep = append(keep, n)
	}
	l.Nodes = keep
	return true
}

// mutators are the template functions that change their arguments in place (sprig's dict
// functions) or call anything (call).
var mutators = map[string]bool{
	"set": true, "unset": true, "merge": true, "mergeOverwrite": true, "mustMerge": true,
	"mustMergeOverwrite": true, "call": true,
}

// callsMutator tells whether a template calls one of the mutators anywhere.
func callsMutator(root parse.Node) bool {
	found := false
	var walk func(parse.Node)
	walk = func(n parse.Node) {
		if n == nil || found {
			return
		}
		switch x := n.(type) {
		case *parse.ListNode:
			if x == nil {
				return
			}
			for _, c := range x.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			walk(x.Pipe)
		case *parse.IfNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.WithNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.RangeNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.TemplateNode:
			walk(x.Pipe)
		case *parse.PipeNode:
			if x == nil {
				return
			}
			inspect(x, func(m parse.Node) {
				if id, ok := m.(*parse.IdentifierNode); ok && mutators[id.Ident] {
					found = true
				}
			})
		}
	}
	walk(root)
	return found
}

// assigns tells whether a part sets a variable that may live outside it ($x = …).
func assigns(n parse.Node) bool {
	found := false
	var walk func(parse.Node)
	walk = func(n parse.Node) {
		if n == nil || found {
			return
		}
		switch x := n.(type) {
		case *parse.ListNode:
			if x == nil {
				return
			}
			for _, c := range x.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			found = found || x.Pipe.IsAssign
		case *parse.IfNode:
			found = found || x.Pipe.IsAssign
			walk(x.List)
			walk(x.ElseList)
		case *parse.WithNode:
			found = found || x.Pipe.IsAssign
			walk(x.List)
			walk(x.ElseList)
		case *parse.RangeNode:
			found = found || x.Pipe.IsAssign
			walk(x.List)
			walk(x.ElseList)
		}
	}
	walk(n)
	return found
}

// valueVaries tells whether a printed or assigned value can differ from mail to mail: it reads
// the subscriber or the time, or a variable that does. Unlike a condition, a plain variable is
// not enough ({{ $leave }} after {{ $leave := UnsubscribeURL . }} is the same for everyone).
func valueVaries(p *parse.PipeNode, varying map[string]bool, subj bool) bool {
	dep := false
	inspect(p, func(n parse.Node) {
		switch x := n.(type) {
		case *parse.VariableNode:
			// A field path through any variable: $ or one that holds the message ($mail := .),
			// whose .Subject or .Subscriber differ as the message's do (LST-48 review).
			if varying[x.Ident[0]] || varyingFields(x.Ident[1:], subj) {
				dep = true
			}
		case *parse.FieldNode:
			dep = dep || varyingFields(x.Ident, subj)
		case *parse.ChainNode:
			dep = dep || varyingFields(x.Field, subj)
		case *parse.IdentifierNode:
			if x.Ident == "Date" || x.Ident == "now" {
				dep = true
			}
		}
	})
	return dep
}

// dependent tells whether a condition can differ from mail to mail: it reads the subscriber, a
// variable (which may hold anything), or the time (Date, now, the campaign's times).
func dependent(p *parse.PipeNode, subj bool) bool {
	dep := false
	inspect(p, func(n parse.Node) {
		switch x := n.(type) {
		case *parse.VariableNode:
			dep = true
		case *parse.FieldNode:
			dep = dep || varyingFields(x.Ident, subj)
		case *parse.ChainNode:
			dep = dep || varyingFields(x.Field, subj)
		case *parse.IdentifierNode:
			if x.Ident == "Date" || x.Ident == "now" {
				dep = true
			}
		}
	})
	return dep
}

// varyingFields tells whether a field path can read something that differs from mail to mail:
// the subscriber, the campaign's times, or what the message renders for each recipient — the
// body and alternative body, and the rendered subject when subj (the subject template differs
// per mail: LST-48 reviews, a body or header that chose by a subject made per recipient, and one
// that chose by a subject the same for everyone, which was then shown as varying). A name that
// is also a campaign field (.Campaign.Subject) is taken the same way: that only makes the check
// warn, never pass.
func varyingFields(ids []string, subj bool) bool {
	for _, id := range ids {
		switch id {
		case "Subscriber", "UpdatedAt", "StartedAt", "Sent", "ToSend", "Body", "AltBody":
			return true
		case "Subject":
			if subj {
				return true
			}
		}
	}
	return false
}

// subjectVaries tells whether the campaign's subject template can render otherwise from mail to
// mail, read as the sender compiles it (the shorthands such as {{ MessageURL }} given the message:
// LST-48 review). See templateVaries; with withTime the time counts too. A subject that cannot be
// parsed is taken as varying.
func (m *Manager) subjectVaries(c *models.Campaign, withTime bool) bool {
	subj := models.ExpandTplFuncs(c.Subject)
	if subj == "" {
		return false
	}
	return m.templateVaries(c, subj, false, withTime)
}

// templateVaries tells whether a template text reads something that differs from mail to mail:
// a field that does (varyingFields, with .Subject when subj), the message itself passed on (to
// UnsubscribeURL, MessageURL and the like, which print the subscriber's links), or, with
// withTime, the time. It follows what is rendered, from the main template: what "." and "$" are
// follows the template — inside a with or range over something other than the message "." is
// that value (LST-48 review: {{ with .Campaign.Attribs.edition }}{{ . }}{{ end }} is the same
// for everyone); a variable given the message, also where a with, if or range declares it
// ({{ with $mail := . }}), is the message; a named template gets as "." and "$" what it is called
// with ({{ template "edition" .Campaign.Attribs.edition }}). A text that cannot be parsed is
// taken as varying.
func (m *Manager) templateVaries(c *models.Campaign, text string, subj, withTime bool) bool {
	t, err := template.New("").Funcs(m.TemplateFuncs(c)).Parse(text)
	if err != nil || t.Tree == nil {
		return err != nil
	}
	w := &varyWalker{t: t, subj: subj, withTime: withTime, seen: map[string]bool{}}
	return w.list(t.Tree.Root, true, map[string]bool{"$": true})
}

// varyWalker walks the templates for templateVaries.
type varyWalker struct {
	t              *template.Template
	subj, withTime bool
	// seen: the named templates already walked, by name and whether "." is the message.
	seen map[string]bool
}

// pipe tells whether a pipe reads something that varies, where dotMsg tells whether "." is the
// message and msgVars are the variables that hold it.
func (w *varyWalker) pipe(p *parse.PipeNode, dotMsg bool, msgVars map[string]bool) bool {
	dep := false
	inspect(p, func(n parse.Node) {
		switch x := n.(type) {
		case *parse.DotNode:
			dep = dep || dotMsg
		case *parse.VariableNode:
			if msgVars[x.Ident[0]] {
				dep = dep || len(x.Ident) == 1 || varyingFields(x.Ident[1:], w.subj)
			}
		case *parse.FieldNode:
			dep = dep || varyingFields(x.Ident, w.subj)
		case *parse.ChainNode:
			dep = dep || varyingFields(x.Field, w.subj)
		case *parse.IdentifierNode:
			if w.withTime && (x.Ident == "Date" || x.Ident == "now") {
				dep = true
			}
		}
	})
	return dep
}

// isMsg tells whether a pipe's value is the message itself: "." where it is the message, or a
// variable that holds it.
func isMsg(p *parse.PipeNode, dotMsg bool, msgVars map[string]bool) bool {
	if p == nil || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	switch x := p.Cmds[0].Args[0].(type) {
	case *parse.DotNode:
		return dotMsg
	case *parse.VariableNode:
		return len(x.Ident) == 1 && msgVars[x.Ident[0]]
	}
	return false
}

// declare records the variables a pipe declares or assigns: they hold the message when the
// pipe's value is the message, and no longer otherwise.
func declare(p *parse.PipeNode, msg bool, msgVars map[string]bool) {
	if p == nil {
		return
	}
	for _, v := range p.Decl {
		if msg {
			msgVars[v.Ident[0]] = true
		} else {
			delete(msgVars, v.Ident[0])
		}
	}
}

// list walks a list of nodes.
func (w *varyWalker) list(l *parse.ListNode, dotMsg bool, msgVars map[string]bool) bool {
	if l == nil {
		return false
	}
	for _, n := range l.Nodes {
		switch x := n.(type) {
		case *parse.ActionNode:
			if len(x.Pipe.Decl) > 0 && isMsg(x.Pipe, dotMsg, msgVars) {
				// {{ $mail := . }}: an alias, nothing printed.
				declare(x.Pipe, true, msgVars)
				continue
			}
			if w.pipe(x.Pipe, dotMsg, msgVars) {
				return true
			}
			declare(x.Pipe, false, msgVars)
		case *parse.IfNode:
			msg := isMsg(x.Pipe, dotMsg, msgVars)
			if !msg && w.pipe(x.Pipe, dotMsg, msgVars) {
				return true
			}
			declare(x.Pipe, msg, msgVars)
			if w.list(x.List, dotMsg, msgVars) || w.list(x.ElseList, dotMsg, msgVars) {
				return true
			}
		case *parse.WithNode:
			msg := isMsg(x.Pipe, dotMsg, msgVars)
			if !msg && w.pipe(x.Pipe, dotMsg, msgVars) {
				return true
			}
			declare(x.Pipe, msg, msgVars)
			if w.list(x.List, msg, msgVars) || w.list(x.ElseList, dotMsg, msgVars) {
				return true
			}
		case *parse.RangeNode:
			if w.pipe(x.Pipe, dotMsg, msgVars) {
				return true
			}
			declare(x.Pipe, false, msgVars)
			if w.list(x.List, false, msgVars) || w.list(x.ElseList, dotMsg, msgVars) {
				return true
			}
		case *parse.TemplateNode:
			// The named template gets the value it is called with as "." and "$".
			msg := isMsg(x.Pipe, dotMsg, msgVars)
			if !msg && w.pipe(x.Pipe, dotMsg, msgVars) {
				return true
			}
			key := x.Name + "|" + strconv.FormatBool(msg)
			if w.seen[key] {
				continue
			}
			w.seen[key] = true
			nt := w.t.Lookup(x.Name)
			if nt == nil || nt.Tree == nil {
				continue
			}
			if w.list(nt.Tree.Root, msg, map[string]bool{"$": msg}) {
				return true
			}
		}
	}
	return false
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

// ReadsVarying tells whether a template text (a header, the subject) reads something that
// differs from mail to mail: the subscriber, the campaign's times, or what the message renders
// for each recipient (.Body, .AltBody, and .Subject when the subject template differs per mail),
// directly or through a variable that holds the message (LST-48 review: From and Bcc chosen by a subject a template made per recipient were
// shown as fixed). A text that cannot be parsed is taken as varying.
func (m *Manager) ReadsVarying(c *models.Campaign, text string) bool {
	if !strings.Contains(text, "{{") {
		return false
	}
	// Time is not counted here: the window renders the envelope at two clocks for that.
	return m.templateVaries(c, text, m.subjectVaries(c, false), false)
}
