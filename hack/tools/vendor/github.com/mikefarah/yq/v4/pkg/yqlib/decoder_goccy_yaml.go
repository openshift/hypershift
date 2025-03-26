//go:build !yq_noyaml

//
// NOTE this is still a WIP - not yet ready.
//

package yqlib

import (
	"io"

	yaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

type goccyYamlDecoder struct {
	decoder yaml.Decoder
	cm      yaml.CommentMap
}

func NewGoccyYAMLDecoder() Decoder {
	return &goccyYamlDecoder{}
}

func (dec *goccyYamlDecoder) Init(reader io.Reader) error {
	dec.cm = yaml.CommentMap{}
	dec.decoder = *yaml.NewDecoder(reader, yaml.CommentToMap(dec.cm), yaml.UseOrderedMap())
	return nil
}

func (dec *goccyYamlDecoder) Decode() (*CandidateNode, error) {

	var ast ast.Node

	err := dec.decoder.Decode(&ast)
	if err != nil {
		return nil, err
	}

	candidateNode := &CandidateNode{}
	if err := candidateNode.UnmarshalGoccyYAML(ast, dec.cm); err != nil {
		return nil, err
	}

	return candidateNode, nil
}
