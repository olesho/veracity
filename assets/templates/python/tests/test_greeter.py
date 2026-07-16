from example import EnglishGreeter


def test_greet_default() -> None:
    assert EnglishGreeter().greet("") == "Hello, World!"


def test_greet_named() -> None:
    assert EnglishGreeter().greet("Ada") == "Hello, Ada!"
