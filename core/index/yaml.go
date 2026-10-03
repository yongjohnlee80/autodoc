package index

import (
	"fmt"
	"path"
	"strings"

	pyaml "github.com/yongjohnlee80/golib/parse/yaml"
	"github.com/yongjohnlee80/golib/yaml"
)

func prepareYAML(src []byte, filePath string, limit int) (docMeta, []chunkT) {
	meta := docMeta{title: strings.TrimSuffix(path.Base(filePath), path.Ext(filePath))}
	stream, err := pyaml.Parse(src)
	if err == nil {
		for _, document := range stream.Docs {
			if _, err = yaml.Evaluate(document, yaml.Core); err != nil {
				break
			}
		}
	}
	if err != nil {
		meta.frontmatterErr = err.Error()
		return meta, chunkPlainText(src, meta.title, limit)
	}
	if len(stream.Docs) == 1 && stream.Docs[0].Root != nil && stream.Docs[0].Root.Kind == pyaml.KindMapping {
		for _, pair := range stream.Docs[0].Root.Pairs {
			if pair.Key.Kind == pyaml.KindScalar && string(pair.Key.Value) == "title" && pair.Value.Kind == pyaml.KindScalar {
				if title := strings.TrimSpace(string(pair.Value.Value)); title != "" {
					meta.title = title
				}
				break
			}
		}
	}
	if limit <= 0 {
		limit = maxTokens
	}
	var chunks []chunkT
	var walk func(*pyaml.Node, string)
	walk = func(node *pyaml.Node, keyPath string) {
		if node == nil {
			return
		}
		switch node.Kind {
		case pyaml.KindMapping:
			for _, pair := range node.Pairs {
				key := strings.TrimSpace(string(pair.Key.Value))
				if key == "" {
					key = "key"
				}
				if keyPath != "" {
					key = keyPath + " > " + key
				}
				walk(pair.Value, key)
			}
		case pyaml.KindSequence:
			for index, item := range node.Items {
				key := fmt.Sprintf("[%d]", index)
				if keyPath != "" {
					key = keyPath + " " + key
				}
				walk(item, key)
			}
		case pyaml.KindScalar, pyaml.KindAlias:
			body := strings.TrimSpace(string(node.Value))
			if node.Kind == pyaml.KindAlias {
				body = "*" + node.Alias
			}
			if body == "" {
				return
			}
			crumb := boundedCrumb(meta.title+" > "+keyPath, limit)
			if keyPath == "" {
				crumb = boundedCrumb(meta.title, limit)
			}
			if tokensOf([]byte(crumb+"\n"+body), 0, len(crumb)+1+len(body)) <= limit {
				chunks = append(chunks, newChunk(len(chunks), crumb, body, node.Span.Start, node.Span.End))
				return
			}
			for _, part := range chunkPlainText(src[node.Span.Start:node.Span.End], crumb, limit) {
				chunks = append(chunks, newChunk(len(chunks), crumb, part.body, node.Span.Start+part.byteStart, node.Span.Start+part.byteEnd))
			}
		}
	}
	for index, document := range stream.Docs {
		key := ""
		if len(stream.Docs) > 1 {
			key = fmt.Sprintf("document %d", index+1)
		}
		walk(document.Root, key)
	}
	if len(chunks) == 0 {
		chunks = append(chunks, newChunk(0, boundedCrumb(meta.title, limit), "", 0, 0))
	}
	return meta, chunks
}
