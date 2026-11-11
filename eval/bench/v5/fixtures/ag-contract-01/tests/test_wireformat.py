import pytest

from wireformat import decode, encode


def test_round_trip():
    payload = {"a": 1, "b": [True, None, "x"]}
    assert decode(encode(payload)) == payload


def test_escapes_and_unicode():
    payload = {"text": "line\nbreak \"quoted\" café"}
    assert decode(encode(payload)) == payload


def test_rejects_bad_input():
    with pytest.raises(ValueError):
        decode("not a record")
