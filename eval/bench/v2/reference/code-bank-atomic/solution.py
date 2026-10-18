class Ledger:
    def __init__(self):
        self.entries = []


class Account:
    def __init__(self, ledger=None):
        self._balance = 0
        self.ledger = ledger

    @property
    def balance(self):
        return self._balance

    def _record(self, kind, amount):
        if self.ledger is not None:
            self.ledger.entries.append((kind, amount))

    def deposit(self, n):
        if n < 0:
            raise ValueError("negative deposit")
        self._balance += n
        self._record("deposit", n)

    def withdraw(self, n):
        if n < 0 or n > self._balance:
            raise ValueError("insufficient funds")
        self._balance -= n
        self._record("withdraw", n)

    def transfer(self, other, n):
        if n < 0 or n > self._balance:
            raise ValueError("insufficient funds")
        self._balance -= n
        other._balance += n
        self._record("transfer", n)
