package main

import (
	"errors"
	"math"
	"strconv"
)

// ---- calculator: + - * / parentheses, floats ----
func evalExpr(s string) (float64, error) {
	toks, err := tokenize(s)
	if err != nil {
		return 0, err
	}
	rpn, err := shuntingYard(toks)
	if err != nil {
		return 0, err
	}
	return evalRPN(rpn)
}

func evalRPN(toks []token) (float64, error) {
	var st []float64
	for _, t := range toks {
		if t.typ == tNumber {
			v, err := strconv.ParseFloat(t.val, 64)
			if err != nil {
				return 0, errors.New("bad number")
			}
			st = append(st, v)
			continue
		}
		if t.typ == tOp {
			if len(st) < 2 {
				return 0, errors.New("bad expression")
			}
			b := st[len(st)-1]
			a := st[len(st)-2]
			st = st[:len(st)-2]
			var r float64
			switch t.val {
			case "+":
				r = a + b
			case "-":
				r = a - b
			case "*":
				r = a * b
			case "/":
				if b == 0 {
					return 0, errors.New("division by zero")
				}
				r = a / b
			}
			st = append(st, r)
		}
	}
	if len(st) != 1 {
		return 0, errors.New("bad expression")
	}
	if math.IsInf(st[0], 0) || math.IsNaN(st[0]) {
		return 0, errors.New("bad result")
	}
	return st[0], nil
}

func prec(op string) int {
	switch op {
	case "+", "-":
		return 1
	case "*", "/":
		return 2
	default:
		return 0
	}
}

func rewriteUnaryMinus(toks []token) []token {
	var out []token
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.typ == tOp && t.val == "-" {
			if i == 0 || toks[i-1].typ == tOp || toks[i-1].typ == tLParen {
				// unary minus -> 0 - ...
				out = append(out, token{typ: tNumber, val: "0"})
			}
		}
		out = append(out, t)
	}
	return out
}
