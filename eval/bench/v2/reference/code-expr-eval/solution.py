class _Parser:
    def __init__(self, text):
        self.text = text
        self.i = 0

    def peek(self):
        while self.i < len(self.text) and self.text[self.i].isspace():
            self.i += 1
        return self.text[self.i] if self.i < len(self.text) else ""

    def expr(self):
        value = self.term()
        while self.peek() in ("+", "-"):
            op = self.text[self.i]
            self.i += 1
            rhs = self.term()
            value = value + rhs if op == "+" else value - rhs
        return value

    def term(self):
        value = self.factor()
        while self.peek() in ("*", "/"):
            op = self.text[self.i]
            self.i += 1
            rhs = self.factor()
            if op == "*":
                value *= rhs
            else:
                if rhs == 0:
                    raise ZeroDivisionError("division by zero")
                value //= rhs
        return value

    def factor(self):
        c = self.peek()
        if c == "-":
            self.i += 1
            return -self.factor()
        if c == "(":
            self.i += 1
            value = self.expr()
            if self.peek() != ")":
                raise ValueError("expected )")
            self.i += 1
            return value
        if c.isdigit():
            start = self.i
            while self.i < len(self.text) and self.text[self.i].isdigit():
                self.i += 1
            return int(self.text[start:self.i])
        raise ValueError("unexpected input")


def evaluate(expr):
    parser = _Parser(expr)
    value = parser.expr()
    if parser.peek() != "":
        raise ValueError("trailing input")
    return value
