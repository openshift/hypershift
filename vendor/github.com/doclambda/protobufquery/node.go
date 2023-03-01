package protobufquery

import (
	"bytes"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// A NodeType is the type of a Node.
type NodeType uint

const (
	// DocumentNode is a document object that, as the root of the document tree,
	// provides access to the entire XML document.
	DocumentNode NodeType = iota
	// ElementNode is an element.
	ElementNode
	// TextNode is the text content of a node.
	TextNode
)

// A Node consists of a NodeType and some Data (tag name for
// element nodes, content for text) and are part of a tree of Nodes.
type Node struct {
	Parent, PrevSibling, NextSibling, FirstChild, LastChild *Node

	Type NodeType
	Name string
	Data *protoreflect.Value

	level int
}

// ChildNodes gets all child nodes of the node.
func (n *Node) ChildNodes() []*Node {
	var a []*Node
	for nn := n.FirstChild; nn != nil; nn = nn.NextSibling {
		a = append(a, nn)
	}
	return a
}

// InnerText gets the value of the node and all its child nodes.
func (n *Node) InnerText() string {
	var output func(*bytes.Buffer, *Node)
	output = func(buf *bytes.Buffer, n *Node) {
		if n.Type == TextNode {
			buf.WriteString(n.Data.String())
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			output(buf, child)
		}
	}
	var buf bytes.Buffer
	output(&buf, n)
	return buf.String()
}

func outputXML(buf *bytes.Buffer, n *Node) {
	if n.Type == TextNode {
		buf.WriteString(n.Data.String())
		return
	}

	name := "element"
	if n.Name != "" {
		name = n.Name
	}
	buf.WriteString("<" + name + ">")
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		outputXML(buf, child)
	}
	buf.WriteString("</" + name + ">")
}

// OutputXML prints the XML string.
func (n *Node) OutputXML() string {
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0"?>`)
	for n := n.FirstChild; n != nil; n = n.NextSibling {
		outputXML(&buf, n)
	}
	return buf.String()
}

// SelectElement finds the first of child elements with the
// specified name.
func (n *Node) SelectElement(name string) *Node {
	for nn := n.FirstChild; nn != nil; nn = nn.NextSibling {
		if nn.Name == name {
			return nn
		}
	}
	return nil
}

// Value return the value of the node itself or its 'TextElement' children.
// If `nil`, the value is either really `nil` or there is no matching child.
func (n *Node) Value() interface{} {
	if n.Type == TextNode {
		if n.Data == nil {
			return nil
		}
		return n.Data.Interface()
	}

	result := make([]interface{}, 0)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != TextNode || child.Data == nil {
			continue
		}
		result = append(result, child.Data.Interface())
	}

	if len(result) == 0 {
		return nil
	} else if len(result) == 1 {
		return result[0]
	}
	return result
}

// Parse ProtocolBuffer message.
func Parse(msg protoreflect.Message) (*Node, error) {
	doc := &Node{Type: DocumentNode}
	visit(doc, msg, 1)
	return doc, nil
}

func visit(parent *Node, msg protoreflect.Message, level int) {
	msg.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		traverse(parent, f, v, level)
		return true
	})
}

func traverse(parent *Node, field protoreflect.FieldDescriptor, value protoreflect.Value, level int) {
	node := &Node{Type: ElementNode, Name: string(field.Name()), level: level}
	nodeChildren := 0
	if field.IsList() {
		l := value.List()

		for i := 0; i < l.Len(); i++ {
			subNode := handleValue(field.Kind(), l.Get(i), level+1)
			if subNode.Type == ElementNode {
				// Add element nodes directly to the parent
				subNode.Name = node.Name
				addNode(parent, subNode)
			} else {
				// Add basic nodes to the local collection node
				elementNode := &Node{Type: ElementNode, level: level + 1}
				subNode.level += 2
				addNode(elementNode, subNode)
				addNode(node, elementNode)
				nodeChildren++
			}
		}
	} else {
		subNode := handleValue(field.Kind(), value, level+1)
		if subNode.Type == ElementNode {
			// Add element nodes directly to the parent
			subNode.Name = node.Name
			addNode(parent, subNode)
		} else {
			// Add basic nodes to the local collection node
			addNode(node, subNode)
			nodeChildren++
		}
	}

	// Only add the node if it has children
	if nodeChildren > 0 {
		addNode(parent, node)
	}
}

func handleValue(kind protoreflect.Kind, value protoreflect.Value, level int) *Node {
	var node *Node

	switch kind {
	case protoreflect.MessageKind:
		node = &Node{Type: ElementNode, level: level}
		visit(node, value.Message(), level+1)
	default:
		node = &Node{Type: TextNode, Data: &value, level: level}
	}
	return node
}

func addNode(top, n *Node) {
	if n.level == top.level {
		top.NextSibling = n
		n.PrevSibling = top
		n.Parent = top.Parent
		if top.Parent != nil {
			top.Parent.LastChild = n
		}
	} else if n.level > top.level {
		n.Parent = top
		if top.FirstChild == nil {
			top.FirstChild = n
			top.LastChild = n
		} else {
			t := top.LastChild
			t.NextSibling = n
			n.PrevSibling = t
			top.LastChild = n
		}
	}
}
