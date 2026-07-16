"""The sample module: a Greeter Protocol boundary and an English implementation."""

from typing import Protocol


class Greeter(Protocol):
    """Greeter produces a greeting for a name. It is the module's boundary."""

    def greet(self, name: str) -> str:
        """Return a greeting for name."""
        ...


class EnglishGreeter:
    """EnglishGreeter greets in English."""

    def greet(self, name: str) -> str:
        """Return an English greeting, defaulting an empty name to "World"."""
        return f"Hello, {name or 'World'}!"
