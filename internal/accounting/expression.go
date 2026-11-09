package accounting

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"text/scanner"
	"time"
)

// expression implements the numeric, conditional subset of new-api v1 billing
// expressions. Decimal literals and arithmetic remain exact throughout pricing.
type expression struct {
	op   string
	text string
	args []*expression
}

type expressionParser struct {
	tokens []string
	pos    int
	refs   map[string]bool
}

func compileExpression(source string) (*expression, map[string]bool, error) {
	if len(source) > 16384 {
		return nil, nil, fmt.Errorf("billing expression is too long")
	}
	var s scanner.Scanner
	s.Init(strings.NewReader(source))
	s.Mode = scanner.ScanIdents | scanner.ScanInts | scanner.ScanFloats | scanner.ScanStrings
	var scanErr error
	s.Error = func(_ *scanner.Scanner, msg string) { scanErr = fmt.Errorf("billing expression: %s", msg) }
	p := expressionParser{refs: make(map[string]bool)}
	for tok := s.Scan(); tok != scanner.EOF; tok = s.Scan() {
		p.tokens = append(p.tokens, s.TokenText())
	}
	if scanErr != nil {
		return nil, nil, scanErr
	}
	// scanner emits punctuation individually; join the supported two-byte operators.
	var joined []string
	for i := 0; i < len(p.tokens); i++ {
		t := p.tokens[i]
		if i+1 < len(p.tokens) && strings.Contains(" <= >= == != && || ", " "+t+p.tokens[i+1]+" ") {
			t += p.tokens[i+1]
			i++
		}
		joined = append(joined, t)
	}
	p.tokens = joined
	e, errParse := p.parse(0, 0)
	if errParse != nil {
		return nil, nil, errParse
	}
	if p.pos != len(p.tokens) {
		return nil, nil, fmt.Errorf("unexpected billing token %q", p.tokens[p.pos])
	}
	return e, p.refs, nil
}

func precedence(op string) int {
	switch op {
	case "||":
		return 1
	case "&&":
		return 2
	case "==", "!=", "<", "<=", ">", ">=":
		return 3
	case "+", "-":
		return 4
	case "*", "/":
		return 5
	}
	return -1
}

func (p *expressionParser) take(t string) bool {
	if p.pos < len(p.tokens) && p.tokens[p.pos] == t {
		p.pos++
		return true
	}
	return false
}

func (p *expressionParser) parse(minPrec, depth int) (*expression, error) {
	if depth > 64 || p.pos >= len(p.tokens) {
		return nil, fmt.Errorf("invalid billing expression nesting or operand")
	}
	t := p.tokens[p.pos]
	p.pos++
	var left *expression
	if t == "(" {
		var errParse error
		left, errParse = p.parse(0, depth+1)
		if errParse != nil {
			return nil, errParse
		}
		if !p.take(")") {
			return nil, fmt.Errorf("missing billing parenthesis")
		}
	} else if t == "-" || t == "!" {
		right, errParse := p.parse(6, depth+1)
		if errParse != nil {
			return nil, errParse
		}
		left = &expression{op: "unary" + t, args: []*expression{right}}
	} else if p.take("(") {
		switch t {
		case "tier", "fixed", "param", "hour", "weekday":
		default:
			return nil, fmt.Errorf("unsupported billing function %q", t)
		}
		left = &expression{op: t}
		for !p.take(")") {
			arg, errParse := p.parse(0, depth+1)
			if errParse != nil {
				return nil, errParse
			}
			left.args = append(left.args, arg)
			if p.take(")") {
				break
			}
			if !p.take(",") {
				return nil, fmt.Errorf("missing billing argument separator")
			}
		}
		want := 1
		if t == "tier" {
			want = 2
		}
		if len(left.args) != want {
			return nil, fmt.Errorf("wrong number of arguments to %s", t)
		}
	} else {
		left = &expression{op: "literal", text: t}
		if strings.HasPrefix(t, "\"") {
			if _, errUnquote := strconv.Unquote(t); errUnquote != nil {
				return nil, errUnquote
			}
		} else if t != "true" && t != "false" {
			if _, ok := decimalLiteral(t); !ok {
				switch t {
				case "p", "c", "cr", "cc", "cc1h", "cc12h", "ai", "ao", "len", "image_count":
				default:
					return nil, fmt.Errorf("unsupported billing variable %q", t)
				}
				p.refs[t] = true
			}
		}
	}
	for p.pos < len(p.tokens) {
		op := p.tokens[p.pos]
		prec := precedence(op)
		if prec < minPrec {
			break
		}
		p.pos++
		right, errParse := p.parse(prec+1, depth+1)
		if errParse != nil {
			return nil, errParse
		}
		left = &expression{op: op, args: []*expression{left, right}}
	}
	if minPrec == 0 && p.take("?") {
		yes, errParse := p.parse(0, depth+1)
		if errParse != nil {
			return nil, errParse
		}
		if !p.take(":") {
			return nil, fmt.Errorf("missing billing conditional branch")
		}
		no, errParse := p.parse(0, depth+1)
		if errParse != nil {
			return nil, errParse
		}
		left = &expression{op: "?", args: []*expression{left, yes, no}}
	}
	return left, nil
}

type expressionValue struct {
	n    *big.Rat
	b    bool
	s    string
	kind byte
}

type priceEnvironment struct {
	tokens map[string]int64
	params map[string]bool
	at     time.Time
	tier   string
}

func number(v int64) expressionValue { return expressionValue{n: new(big.Rat).SetInt64(v), kind: 'n'} }
func boolean(v bool) expressionValue { return expressionValue{b: v, kind: 'b'} }

func (e *expression) eval(env *priceEnvironment) (expressionValue, error) {
	if e.op == "literal" {
		if strings.HasPrefix(e.text, "\"") {
			s, _ := strconv.Unquote(e.text)
			return expressionValue{s: s, kind: 's'}, nil
		}
		if e.text == "true" || e.text == "false" {
			return boolean(e.text == "true"), nil
		}
		if n, ok := decimalLiteral(e.text); ok {
			return expressionValue{n: n, kind: 'n'}, nil
		}
		return number(env.tokens[e.text]), nil
	}
	a, errEval := e.args[0].eval(env)
	if errEval != nil {
		return expressionValue{}, errEval
	}
	switch e.op {
	case "?":
		if a.kind != 'b' {
			return expressionValue{}, fmt.Errorf("non-boolean billing condition")
		}
		if a.b {
			return e.args[1].eval(env)
		}
		return e.args[2].eval(env)
	case "&&", "||":
		if a.kind != 'b' {
			return expressionValue{}, fmt.Errorf("non-boolean billing operand")
		}
		if (e.op == "&&" && !a.b) || (e.op == "||" && a.b) {
			return a, nil
		}
		b, errRight := e.args[1].eval(env)
		if errRight != nil || b.kind != 'b' {
			return expressionValue{}, fmt.Errorf("invalid billing boolean operand")
		}
		return b, nil
	case "unary!":
		if a.kind != 'b' {
			return expressionValue{}, fmt.Errorf("non-boolean billing negation")
		}
		return boolean(!a.b), nil
	case "unary-":
		if a.kind != 'n' {
			return expressionValue{}, fmt.Errorf("non-numeric billing negation")
		}
		a.n.Neg(a.n)
		return a, nil
	case "param":
		if a.kind != 's' {
			return expressionValue{}, fmt.Errorf("invalid billing parameter")
		}
		v, ok := env.params[a.s]
		if !ok {
			return expressionValue{}, fmt.Errorf("missing billing parameter %s", a.s)
		}
		return boolean(v), nil
	case "hour", "weekday":
		if a.kind != 's' {
			return expressionValue{}, fmt.Errorf("invalid billing timezone")
		}
		loc, errLocation := time.LoadLocation(a.s)
		if errLocation != nil {
			return expressionValue{}, fmt.Errorf("unsupported billing timezone")
		}
		at := env.at.In(loc)
		if e.op == "hour" {
			return number(int64(at.Hour())), nil
		}
		return number(int64(at.Weekday())), nil
	case "fixed":
		if a.kind != 'n' {
			return expressionValue{}, fmt.Errorf("invalid fixed billing price")
		}
		a.n.Mul(a.n, big.NewRat(1000000, 1))
		return a, nil
	case "tier":
		if a.kind != 's' {
			return expressionValue{}, fmt.Errorf("invalid billing tier")
		}
		env.tier = a.s
		return e.args[1].eval(env)
	}
	b, errRight := e.args[1].eval(env)
	if errRight != nil {
		return expressionValue{}, errRight
	}
	if a.kind == 'b' && b.kind == 'b' && (e.op == "==" || e.op == "!=") {
		equal := a.b == b.b
		return boolean(equal == (e.op == "==")), nil
	}
	if a.kind != 'n' || b.kind != 'n' {
		return expressionValue{}, fmt.Errorf("non-numeric billing arithmetic")
	}
	cmp := a.n.Cmp(b.n)
	switch e.op {
	case "+":
		a.n.Add(a.n, b.n)
	case "-":
		a.n.Sub(a.n, b.n)
	case "*":
		a.n.Mul(a.n, b.n)
	case "/":
		if b.n.Sign() == 0 {
			return expressionValue{}, fmt.Errorf("billing division by zero")
		}
		a.n.Quo(a.n, b.n)
	case "==":
		return boolean(cmp == 0), nil
	case "!=":
		return boolean(cmp != 0), nil
	case "<":
		return boolean(cmp < 0), nil
	case "<=":
		return boolean(cmp <= 0), nil
	case ">":
		return boolean(cmp > 0), nil
	case ">=":
		return boolean(cmp >= 0), nil
	default:
		return expressionValue{}, fmt.Errorf("unsupported billing operator")
	}
	if a.n.Num().BitLen() > 256 || a.n.Denom().BitLen() > 256 {
		return expressionValue{}, fmt.Errorf("billing arithmetic exceeds precision limit")
	}
	return a, nil
}

// Bound decimal exponents before big.Rat allocates their numerator or denominator.
func decimalLiteral(text string) (*big.Rat, bool) {
	if len(text) > 128 {
		return nil, false
	}
	if at := strings.IndexAny(text, "eE"); at >= 0 {
		exponent, errParse := strconv.Atoi(text[at+1:])
		if errParse != nil || exponent < -30 || exponent > 30 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(text)
}
