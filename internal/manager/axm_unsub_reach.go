package manager

import (
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
	for _, t := range msg.Campaign.Tpl.Templates() {
		if t.Tree != nil && !pruneVarying(t.Tree.Root) {
			return false, nil
		}
	}
	if msg.Campaign.AltBodyTpl != nil {
		for _, t := range msg.Campaign.AltBodyTpl.Templates() {
			if t.Tree != nil && !pruneVarying(t.Tree.Root) {
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
// UnsubscribeOnEveryPath), in place. false: a part taken out assigns to a variable.
func pruneVarying(l *parse.ListNode) bool {
	if l == nil {
		return true
	}
	keep := l.Nodes[:0]
	for _, n := range l.Nodes {
		drop := false
		switch x := n.(type) {
		case *parse.IfNode:
			if dependent(x.Pipe) {
				drop = true
			} else if !pruneVarying(x.List) || !pruneVarying(x.ElseList) {
				return false
			}
		case *parse.WithNode:
			if dependent(x.Pipe) {
				drop = true
			} else if !pruneVarying(x.List) || !pruneVarying(x.ElseList) {
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
