package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ---- crypto AES-GCM ----
// Store: nonce(12) || ciphertext+tag
func encryptAESGCM(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return append(nonce, ct...), nil
}

func decryptAESGCM(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce := data[:ns]
	ct := data[ns:]
	return gcm.Open(nil, nonce, ct, nil)
}

func tokenize(s string) ([]token, error) {
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return nil, errors.New("empty expression")
	}
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case (c >= '0' && c <= '9') || c == '.':
			j := i + 1
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				j++
			}
			out = append(out, token{typ: tNumber, val: s[i:j]})
			i = j
		case c == '+' || c == '-' || c == '*' || c == '/':
			out = append(out, token{typ: tOp, val: string(c)})
			i++
		case c == '(':
			out = append(out, token{typ: tLParen, val: "("})
			i++
		case c == ')':
			out = append(out, token{typ: tRParen, val: ")"})
			i++
		default:
			return nil, fmt.Errorf("bad char: %q", c)
		}
	}
	// Handle unary minus by rewriting: (-x) or at start -> (0-x)
	out = rewriteUnaryMinus(out)
	return out, nil
}

func shuntingYard(toks []token) ([]token, error) {
	var out []token
	var stack []token
	for _, t := range toks {
		switch t.typ {
		case tNumber:
			out = append(out, t)
		case tOp:
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				if top.typ == tOp && prec(top.val) >= prec(t.val) {
					out = append(out, top)
					stack = stack[:len(stack)-1]
				} else {
					break
				}
			}
			stack = append(stack, t)
		case tLParen:
			stack = append(stack, t)
		case tRParen:
			found := false
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if top.typ == tLParen {
					found = true
					break
				}
				out = append(out, top)
			}
			if !found {
				return nil, errors.New("mismatched parentheses")
			}
		}
	}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.typ == tLParen || top.typ == tRParen {
			return nil, errors.New("mismatched parentheses")
		}
		out = append(out, top)
	}
	return out, nil
}

func trimFloat(v float64) string {
	// Pretty format: remove trailing zeros
	s := strconv.FormatFloat(v, 'f', -1, 64)
	return s
}
