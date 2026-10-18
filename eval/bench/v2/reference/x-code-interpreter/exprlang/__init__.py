"""A tiny statement language: let bindings and arithmetic.

evaluate(source) returns the value of the last statement. Statements are
separated by `;`. Supported: integer literals, identifiers, let bindings,
+ - * /, parentheses, unary minus. Malformed input, undefined variables, and
division by zero raise ExprError.
"""


class ExprError(ValueError):
    pass


class _Lexer:
    def __init__(self, text):
        self.text = text
        self.i = 0

    def _skip(self):
        while self.i < len(self.text) and self.text[self.i].isspace():
            self.i += 1

    def next(self):
        self._skip()
        if self.i >= len(self.text):
            return ("EOF", None)
        c = self.text[self.i]
        if c.isdigit():
            j = self.i
            while self.i < len(self.text) and self.text[self.i].isdigit():
                self.i += 1
            return ("NUMBER", int(self.text[j:self.i]))
        if c.isalpha() or c == "_":
            j = self.i
            while self.i < len(self.text) and (self.text[self.i].isalnum() or self.text[self.i] == "_"):
                self.i += 1
            return ("IDENT", self.text[j:self.i])
        if c in "+-*/=;()":
            self.i += 1
            return (c, c)
        raise ExprError("unexpected character %r" % c)


class _Parser:
    def __init__(self, text):
        self.lex = _Lexer(text)
        self.tok = self.lex.next()

    def advance(self):
        tok = self.tok
        self.tok = self.lex.next()
        return tok

    def expect(self, kind):
        if self.tok[0] != kind:
            raise ExprError("expected %s, got %r" % (kind, self.tok))
        return self.advance()

    def program(self):
        stmts = []
        while self.tok[0] != "EOF":
            if self.tok[0] == "IDENT" and self.tok[1] == "let":
                self.advance()
                name = self.expect("IDENT")[1]
                self.expect("=")
                stmts.append(("let", name, self.expr()))
            else:
                stmts.append(("expr", self.expr()))
            if self.tok[0] == ";":
                self.advance()
            elif self.tok[0] != "EOF":
                raise ExprError("expected ; or EOF")
        return stmts

    def expr(self):
        node = self.term()
        while self.tok[0] in ("+", "-"):
            op = self.advance()[0]
            node = (op, node, self.term())
        return node

    def term(self):
        node = self.factor()
        while self.tok[0] in ("*", "/"):
            op = self.advance()[0]
            node = (op, node, self.factor())
        return node

    def factor(self):
        kind, val = self.tok
        if kind == "NUMBER":
            self.advance()
            return ("num", val)
        if kind == "IDENT":
            self.advance()
            return ("var", val)
        if kind == "-":
            self.advance()
            return ("neg", self.factor())
        if kind == "(":
            self.advance()
            node = self.expr()
            self.expect(")")
            return node
        raise ExprError("unexpected token %r" % (self.tok,))


def _eval(node, env):
    kind = node[0]
    if kind == "num":
        return node[1]
    if kind == "var":
        if node[1] not in env:
            raise ExprError("undefined variable %s" % node[1])
        return env[node[1]]
    if kind == "neg":
        return -_eval(node[1], env)
    if kind == "+":
        return _eval(node[1], env) + _eval(node[2], env)
    if kind == "-":
        return _eval(node[1], env) - _eval(node[2], env)
    if kind == "*":
        return _eval(node[1], env) * _eval(node[2], env)
    if kind == "/":
        right = _eval(node[2], env)
        if right == 0:
            raise ExprError("division by zero")
        return _eval(node[1], env) / right
    raise ExprError("bad node")


def evaluate(source):
    if not isinstance(source, str):
        raise ExprError("source must be a string")
    stmts = _Parser(source).program()
    env = {}
    result = 0
    for stmt in stmts:
        if stmt[0] == "let":
            env[stmt[1]] = _eval(stmt[2], env)
            result = env[stmt[1]]
        else:
            result = _eval(stmt[1], env)
    return result
