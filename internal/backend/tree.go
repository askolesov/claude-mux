package backend

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The sidebar is a tree: the environment at its root, groups that nest to
// any depth, and tabs as leaves. Groups and tabs mix in one order. Each
// node is stored flat with its parent ("" for the root) and an order key,
// a fractional index: a move gives one node a key between its new
// neighbours' and leaves every other node alone.

var errCycle = errors.New("a group can't go into itself")

// Group is a container of groups and tabs.
type Group struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"` // "" is the environment
	Order  string `json:"order"`
}

// node is what tabs and groups share: where they are.
type node struct {
	id     string
	parent *string
	order  *string
}

// nodes lists every tab and group, for placing them. The caller holds m.mu;
// a tab's Parent and Order are written with both m.mu and its own lock held.
func (m *Manager) nodes() []node {
	out := make([]node, 0, len(m.sessions)+len(m.groups))
	for _, s := range m.sessions {
		out = append(out, node{s.ID, &s.Parent, &s.Order})
	}
	for _, g := range m.groups {
		out = append(out, node{g.ID, &g.Parent, &g.Order})
	}
	return out
}

// siblings are parent's children in order, without skip.
func (m *Manager) siblings(parent, skip string) []node {
	var out []node
	for _, n := range m.nodes() {
		if *n.parent == parent && n.id != skip {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b node) int { return strings.Compare(*a.order, *b.order) })
	return out
}

func (m *Manager) group(id string) *Group {
	for _, g := range m.groups {
		if g.ID == id {
			return g
		}
	}
	return nil
}

// validParent checks that parent is the root or a group, and, for a group
// being moved, not the group itself or inside it.
func (m *Manager) validParent(parent, moving string) error {
	for p := parent; p != ""; {
		if p == moving {
			return errCycle
		}
		g := m.group(p)
		if g == nil {
			return fmt.Errorf("no group %q", p)
		}
		p = g.Parent
	}
	return nil
}

// place finds the order key for a node going into parent: before the
// sibling before, after the sibling after, or at the end when both are "".
func (m *Manager) place(id, parent, before, after string) (string, error) {
	sib := m.siblings(parent, id)
	at := len(sib)
	if anchor := before + after; anchor != "" {
		at = slices.IndexFunc(sib, func(n node) bool { return n.id == anchor })
		if at < 0 {
			return "", fmt.Errorf("%q isn't in that group", anchor)
		}
		if after != "" {
			at++
		}
	}
	lo, hi := "", ""
	if at > 0 {
		lo = *sib[at-1].order
	}
	if at < len(sib) {
		hi = *sib[at].order
	}
	return keyBetween(lo, hi), nil
}

// Move puts a tab or a group into parent, before or after one of its
// children, or at its end. A group takes everything in it along.
func (m *Manager) Move(id, parent, before, after string) error {
	if before != "" && after != "" {
		return errors.New("give before or after, not both")
	}
	m.mu.Lock()
	err := m.validParent(parent, id)
	var key string
	if err == nil {
		key, err = m.place(id, parent, before, after)
	}
	if err == nil {
		err = m.setPlace(id, parent, key)
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.save()
	m.changes.Notify()
	return nil
}

// setPlace is called with m.mu held.
func (m *Manager) setPlace(id, parent, key string) error {
	if g := m.group(id); g != nil {
		g.Parent, g.Order = parent, key
		return nil
	}
	for _, s := range m.sessions {
		if s.ID == id {
			s.mu.Lock()
			s.Parent, s.Order = parent, key
			s.mu.Unlock()
			return nil
		}
	}
	return errNotFound
}

// CreateGroup makes a group in parent, after a child of it or at its end.
func (m *Manager) CreateGroup(name, parent, after string) (Group, error) {
	if name = strings.TrimSpace(name); name == "" {
		return Group{}, errors.New("a group needs a name")
	}
	m.mu.Lock()
	g := &Group{ID: newID(), Name: name, Parent: parent}
	err := m.validParent(parent, "")
	if err == nil {
		g.Order, err = m.place(g.ID, parent, "", after)
	}
	if err == nil {
		m.groups = append(m.groups, g)
	}
	m.mu.Unlock()
	if err != nil {
		return Group{}, err
	}
	m.save()
	m.changes.Notify()
	return *g, nil
}

// RenameGroup gives a group a new name.
func (m *Manager) RenameGroup(id, name string) error {
	if name = strings.TrimSpace(name); name == "" {
		return errors.New("a group needs a name")
	}
	m.mu.Lock()
	g := m.group(id)
	if g != nil {
		g.Name = name
	}
	m.mu.Unlock()
	if g == nil {
		return errNotFound
	}
	m.save()
	m.changes.Notify()
	return nil
}

// DeleteGroup removes a group. What it held moves up to its parent, in its
// place and in its order; no tab is closed.
func (m *Manager) DeleteGroup(id string) error {
	m.mu.Lock()
	g := m.group(id)
	if g == nil {
		m.mu.Unlock()
		return errNotFound
	}
	kids := m.siblings(id, "")
	after := g.ID
	for _, k := range kids {
		key, _ := m.place(k.id, g.Parent, "", after) // after is always a sibling there: g, then the kid before
		*k.parent, *k.order = g.Parent, key
		after = k.id
	}
	m.groups = slices.DeleteFunc(m.groups, func(x *Group) bool { return x == g })
	m.mu.Unlock()
	m.save()
	m.changes.Notify()
	return nil
}

// Groups is every group, in no order: each has its parent and order key.
func (m *Manager) Groups() []Group {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Group, len(m.groups))
	for i, g := range m.groups {
		out[i] = *g
	}
	return out
}

// Order keys are base-62 fractions: "V" is 0.5, "0V" smaller, "Vk" larger.
// They compare as plain strings, the same in Go and in the page, and
// never end in "0", so there is always room between two of them.
const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// keyBetween is a key strictly between lo and hi; "" is the open end on
// either side.
func keyBetween(lo, hi string) string {
	// Past a common prefix only the rest matters.
	n := 0
	for n < len(hi) && digitAt(lo, n) == hi[n] {
		n++
	}
	if n > 0 {
		return hi[:n] + keyBetween(tail(lo, n), hi[n:])
	}
	a := strings.IndexByte(digits, digitAt(lo, 0))
	b := len(digits)
	if hi != "" {
		b = strings.IndexByte(digits, hi[0])
	}
	if b-a > 1 {
		return string(digits[(a+b)/2])
	}
	// Neighbouring digits: a longer hi has room under its first digit,
	// else go one digit deeper above lo.
	if len(hi) > 1 {
		return hi[:1]
	}
	return string(digits[a]) + keyBetween(tail(lo, 1), "")
}

func digitAt(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return '0'
}

func tail(s string, n int) string {
	if n >= len(s) {
		return ""
	}
	return s[n:]
}
