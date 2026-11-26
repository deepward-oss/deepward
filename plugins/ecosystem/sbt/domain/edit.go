package domain

import (
	"fmt"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

// TokenKind classifies Scala tokens.
type TokenKind int

// Token kinds.
const (
	TokString TokenKind = iota // plain "..." or """...""" literal
	TokInterpolated
	TokIdent
	TokOp
	TokPunct
)

// Token is a lexical token; for strings Start/End delimit the content
// (without quotes).
type Token struct {
	Kind       TokenKind
	Text       string
	Start, End int
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') || c == '.' }

func isOp(c byte) bool { return strings.IndexByte("%+-*/=<>!&|^~:#@\\?", c) >= 0 }

// Tokenize lexes Scala source: string literals (including triple-quoted and
// interpolated), identifiers (dots included, so Versions.circe is one
// token), operators and punctuation. Comments and character literals are
// skipped. It never fails; malformed input yields a best-effort stream.
func Tokenize(src []byte) []Token {
	var out []Token
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			depth := 0
			for i < n {
				if i+1 < n && src[i] == '/' && src[i+1] == '*' {
					depth++
					i += 2
					continue
				}
				if i+1 < n && src[i] == '*' && src[i+1] == '/' {
					depth--
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				i++
			}
		case c == '"':
			tok, next := lexString(src, i, TokString)
			out = append(out, tok)
			i = next
		case c == '\'':
			// Character literal ('a', '\n') or symbol ('sym): skip.
			switch {
			case i+2 < n && src[i+2] == '\'':
				i += 3
			case i+3 < n && src[i+1] == '\\' && src[i+3] == '\'':
				i += 4
			default:
				i++
			}
		case isIdentStart(c):
			start := i
			for i < n && isIdentPart(src[i]) {
				i++
			}
			if i < n && src[i] == '"' {
				tok, next := lexString(src, i, TokInterpolated)
				tok.Text = string(src[start:i]) + tok.Text
				out = append(out, tok)
				i = next
				continue
			}
			out = append(out, Token{Kind: TokIdent, Text: string(src[start:i]), Start: start, End: i})
		case isOp(c):
			start := i
			for i < n && isOp(src[i]) {
				i++
			}
			out = append(out, Token{Kind: TokOp, Text: string(src[start:i]), Start: start, End: i})
		default:
			out = append(out, Token{Kind: TokPunct, Text: string(c), Start: i, End: i + 1})
			i++
		}
	}
	return out
}

func lexString(src []byte, i int, kind TokenKind) (Token, int) {
	n := len(src)
	if i+2 < n && src[i+1] == '"' && src[i+2] == '"' {
		start := i + 3
		j := start
		for j+2 < n && (src[j] != '"' || src[j+1] != '"' || src[j+2] != '"') {
			j++
		}
		if j+2 >= n {
			return Token{Kind: kind, Text: string(src[start:]), Start: start, End: n}, n
		}
		return Token{Kind: kind, Text: string(src[start:j]), Start: start, End: j}, j + 3
	}
	start := i + 1
	j := start
	for j < n && src[j] != '"' && src[j] != '\n' {
		if src[j] == '\\' {
			j++
		}
		j++
	}
	if j > n {
		j = n
	}
	end := j
	if j < n && src[j] == '"' {
		j++
	}
	return Token{Kind: kind, Text: string(src[start:end]), Start: start, End: end}, j
}

// File is a Scala build file.
type File struct {
	Path string
	Data []byte
}

type valDef struct {
	file  string
	token Token
}

// Plan computes edits that set org:name to change.To in the build files.
// Transitive pins are never edited automatically: they return a manual
// change with a ready-to-paste dependencyOverrides line.
func Plan(files []File, change sdk.PackageChange, org, name string) ([]sdk.TextEdit, *sdk.ManualChange) {
	base, suffix := purl.SplitScala(name)
	suggestion := overrideLine(org, base, suffix, name, change.To)
	if change.Pin {
		return nil, &sdk.ManualChange{Change: change, Reason: "sbt transitive versions are not pinned automatically", Suggestion: suggestion}
	}
	tokens := map[string][]Token{}
	vals := map[string][]valDef{}
	for _, f := range files {
		toks := Tokenize(f.Data)
		tokens[f.Path] = toks
		for i := 0; i+3 < len(toks); i++ {
			if toks[i].Kind == TokIdent && toks[i].Text == "val" && toks[i+1].Kind == TokIdent &&
				toks[i+2].Kind == TokOp && toks[i+2].Text == "=" && toks[i+3].Kind == TokString {
				vals[toks[i+1].Text] = append(vals[toks[i+1].Text], valDef{file: f.Path, token: toks[i+3]})
			}
		}
	}
	var edits []sdk.TextEdit
	seen := map[string]bool{}
	add := func(file string, t Token) {
		key := fmt.Sprintf("%s:%d", file, t.Start)
		if !seen[key] {
			seen[key] = true
			edits = append(edits, sdk.TextEdit{Path: file, Start: t.Start, End: t.End, Replacement: change.To, Expected: t.Text})
		}
	}
	for _, f := range files {
		toks := tokens[f.Path]
		for i := 0; i+4 < len(toks); i++ {
			o, op1, n, op2, v := toks[i], toks[i+1], toks[i+2], toks[i+3], toks[i+4]
			if o.Kind != TokString || n.Kind != TokString || op1.Kind != TokOp || op2.Kind != TokOp || op2.Text != "%" || o.Text != org {
				continue
			}
			matches := false
			switch op1.Text {
			case "%":
				matches = n.Text == name
			case "%%", "%%%":
				matches = n.Text == base && suffix != ""
			}
			if !matches {
				continue
			}
			switch v.Kind {
			case TokString:
				add(f.Path, v)
			case TokIdent:
				key := v.Text
				if i := strings.LastIndex(key, "."); i >= 0 {
					key = key[i+1:]
				}
				defs := vals[key]
				if len(defs) != 1 {
					reason := fmt.Sprintf("the version comes from %s, which is not a single string val in the build files", v.Text)
					return nil, &sdk.ManualChange{Change: change, Reason: reason, Suggestion: suggestion}
				}
				add(defs[0].file, defs[0].token)
			default:
				return nil, &sdk.ManualChange{Change: change, Reason: "the version is not a literal or a val", Suggestion: suggestion}
			}
		}
	}
	if len(edits) == 0 {
		return nil, &sdk.ManualChange{Change: change, Reason: fmt.Sprintf("%s:%s is not declared in the build files", org, name), Suggestion: suggestion}
	}
	return edits, nil
}

func overrideLine(org, base, suffix, name, to string) string {
	if suffix != "" && strings.HasPrefix(suffix, "_2.") || suffix == "_3" {
		return fmt.Sprintf("dependencyOverrides += %q %%%% %q %% %q", org, base, to)
	}
	return fmt.Sprintf("dependencyOverrides += %q %% %q %% %q", org, name, to)
}
