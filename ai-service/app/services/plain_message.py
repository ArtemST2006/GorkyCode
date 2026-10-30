"""Keep user-facing messages as one line of plain text."""

import re
import unicodedata


_MARKDOWN_LINK = re.compile(r"!?\[([^\]]+)\]\([^)]*\)")
_HTML_TAG = re.compile(r"<[^>]+>")
_MARKDOWN_PREFIX = re.compile(r"(?m)^[ \t]{0,3}(?:#{1,6}[ \t]+|>[ \t]+|[-*•][ \t]+)")
_SINGLE_ITALIC = re.compile(r"(?<!\w)\*([^*\n]+)\*(?!\w)")


def plain_message(value: str) -> str:
    value = _MARKDOWN_LINK.sub(r"\1", value)
    value = _HTML_TAG.sub(" ", value)
    value = _MARKDOWN_PREFIX.sub("", value)
    value = value.replace("\\n", " ").replace("\\t", " ")
    value = value.replace("—", "-").replace("–", "-").replace("•", " ")
    value = value.replace("№", "номер ")
    value = value.replace("**", "").replace("__", "").replace("~~", "").replace("`", "")
    value = _SINGLE_ITALIC.sub(r"\1", value)
    value = "".join(
        " " if unicodedata.category(char) in {"So", "Sk", "Cf", "Me"}
        or char == "\ufe0f" else char
        for char in value
    )
    return " ".join(value.split())
