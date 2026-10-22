package api

import (
	"errors"
	"io"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// hiddenYAML replaces a YAML file that does not parse, for a caller who
// cannot read secrets. Without a parse there is no telling where its values
// are. It contains the mask so writing it back is refused like any other
// masked content.
const hiddenYAML = "# " + maskedEnvValue + " This file is not valid YAML, so it is hidden from accounts that cannot read env values.\n"

// isYAMLFile matches the files that can hold a Compose project.
func isYAMLFile(rel string) bool {
	lower := strings.ToLower(rel)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}

// envRefRe matches a value that is only a reference to another variable,
// such as ${DB_PASSWORD}. It names a secret without holding one, and it is
// what a viewer needs to see to understand where a value comes from.
var envRefRe = regexp.MustCompile(`^\$(\{[A-Za-z_][A-Za-z0-9_]*\}|[A-Za-z_][A-Za-z0-9_]*)$`)

// maskComposeFile hides the values a Compose file sets inline: each
// service's environment and build args, and inline config content, along
// with the comments written among them. Values reached through YAML anchors,
// aliases and merge keys are masked where they are written. Keys, other
// comments and the structure stay, so the file still reads as the project.
// A masked file is re-encoded from its parsed form rather than edited line
// by line, so a value can never be shown in part, at the cost of the file's
// original layout. A file with nothing to mask comes back unchanged.
func maskComposeFile(content string) string {
	dec := yaml.NewDecoder(strings.NewReader(content))
	m := composeMasker{seen: map[*yaml.Node]bool{}}
	var docs []*yaml.Node
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return hiddenYAML
		}
		if len(doc.Content) == 1 {
			m.document(doc.Content[0])
		}
		docs = append(docs, &doc)
	}
	if !m.changed {
		return content
	}
	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	for _, doc := range docs {
		plainMergeKeys(doc)
		if err := enc.Encode(doc); err != nil {
			return hiddenYAML
		}
	}
	if err := enc.Close(); err != nil {
		return hiddenYAML
	}
	return out.String()
}

type composeMasker struct {
	seen    map[*yaml.Node]bool
	changed bool
}

func (m *composeMasker) document(root *yaml.Node) {
	root = resolve(root)
	if root.Kind != yaml.MappingNode {
		return
	}
	for _, services := range lookupAll(root, "services") {
		for _, svc := range mappingValues(services[1]) {
			m.block(svc, "environment")
			for _, build := range lookupAll(svc, "build") {
				m.block(build[1], "args")
			}
		}
	}
	for _, configs := range lookupAll(root, "configs") {
		for _, cfg := range mappingValues(configs[1]) {
			m.block(cfg, "content")
		}
	}
}

// mappingValues returns the values of mapping n, merge keys included.
func mappingValues(n *yaml.Node) []*yaml.Node {
	var out []*yaml.Node
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "<<" {
			for _, merged := range mergeSources(n.Content[i+1]) {
				out = append(out, mappingValues(merged)...)
			}
			continue
		}
		out = append(out, n.Content[i+1])
	}
	return out
}

// mergeSources returns the mappings a merge key's value names: one, or a
// list of them.
func mergeSources(v *yaml.Node) []*yaml.Node {
	if r := resolve(v); r != nil && r.Kind == yaml.SequenceNode {
		return r.Content
	}
	return []*yaml.Node{v}
}

// block masks the value of key in mapping n, wherever a merge key put it.
// Every occurrence is masked, not only the one that wins the merge, since
// the others are still written in the file.
func (m *composeMasker) block(n *yaml.Node, key string) {
	for _, pair := range lookupAll(n, key) {
		uncomment(pair[0])
		if key == "content" {
			m.scalar(pair[1])
		} else {
			m.values(pair[1])
		}
	}
}

// values masks an environment or args block, in map or list form.
func (m *composeMasker) values(n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.AliasNode {
		m.values(n.Alias)
		return
	}
	if n.Kind == yaml.ScalarNode {
		m.scalar(n)
		return
	}
	if m.seen[n] {
		return
	}
	m.seen[n] = true
	uncomment(n)
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			uncomment(k)
			if k.Value == "<<" {
				uncomment(resolve(v))
				for _, merged := range mergeSources(v) {
					m.values(merged)
				}
				continue
			}
			m.scalar(v)
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			r := resolve(item)
			if r.Kind != yaml.ScalarNode {
				m.scalar(item)
				continue
			}
			uncomment(r)
			key, value, ok := strings.Cut(r.Value, "=")
			if !ok {
				continue // KEY alone passes the variable through from the host
			}
			if value == "" || envRefRe.MatchString(value) {
				continue
			}
			m.replace(r, key+"="+maskedEnvValue)
		}
	}
}

// scalar masks one value, following an alias to where it is written. A
// mapping or list where a value belongs is replaced whole.
func (m *composeMasker) scalar(n *yaml.Node) {
	n = resolve(n)
	if n == nil || m.seen[n] {
		return
	}
	m.seen[n] = true
	uncomment(n)
	if n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || n.Value == "" || envRefRe.MatchString(n.Value)) {
		return
	}
	m.replace(n, maskedEnvValue)
}

// replace turns n into a quoted string scalar, keeping its anchor so any
// aliases to it still resolve. Quoting matters: a bare ******** would read
// as an alias.
func (m *composeMasker) replace(n *yaml.Node, value string) {
	anchor := n.Anchor
	*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle, Anchor: anchor}
	m.changed = true
}

// uncomment drops the comments on n. Inside an environment block they are
// as likely as the values to hold a secret: an old password, a line
// commented out.
func uncomment(n *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
}

// plainMergeKeys clears the tag the decoder gives each << key, which the
// encoder would otherwise write out as !!merge <<.
func plainMergeKeys(n *yaml.Node) {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i]; k.Value == "<<" && k.Tag == "!!merge" {
				k.Tag = ""
			}
		}
	}
	for _, c := range n.Content {
		plainMergeKeys(c)
	}
}

func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// lookup returns the value for key in mapping n, or nil.
func lookup(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// lookupAll returns every key and value pair for key in mapping n,
// including those pulled in through merge keys, however deeply nested.
func lookupAll(n *yaml.Node, key string) [][2]*yaml.Node {
	var out [][2]*yaml.Node
	visited := map[*yaml.Node]bool{}
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		n = resolve(n)
		if n == nil || visited[n] {
			return
		}
		visited[n] = true
		switch n.Kind {
		case yaml.SequenceNode:
			for _, item := range n.Content {
				walk(item)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				switch n.Content[i].Value {
				case key:
					out = append(out, [2]*yaml.Node{n.Content[i], n.Content[i+1]})
				case "<<":
					walk(n.Content[i+1])
				}
			}
		}
	}
	walk(n)
	return out
}
