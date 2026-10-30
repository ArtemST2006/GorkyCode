"""Bounded checks for accidental disclosure; not an authorization boundary."""

import base64
import re
from functools import lru_cache
from importlib.resources import files

from app.core.config import Settings
from app.core.errors import ServiceError


def _words(text: str) -> str:
    return " ".join(re.findall(r"[\w]+", text.casefold()))


@lru_cache(maxsize=1)
def _protected_fragments() -> frozenset[str]:
    fragments = set()
    for package, resource in (
        ("app.llm", "prompts/job.txt"),
        ("app.security", "prompts/action_guard.txt"),
    ):
        tokens = _words(files(package).joinpath(resource).read_text(encoding="utf-8")).split()
        for index in range(max(0, len(tokens) - 11)):
            fragment = " ".join(tokens[index:index + 12])
            if len(fragment) >= 60:
                fragments.add(fragment)
    return frozenset(fragments)


class DisclosurePolicy:
    def __init__(self, settings: Settings):
        secret = settings.cloudru_api_key
        value = secret.get_secret_value() if secret else ""
        self._secrets = tuple(dict.fromkeys((
            value, base64.b64encode(value.encode()).decode(), value.encode().hex(),
        ))) if value else ()

    @staticmethod
    def _reject() -> None:
        raise ServiceError(400, "unsupported_intent", "Unsupported request",
                           "This response cannot be returned safely.")

    def check_credentials(self, value: object) -> None:
        # Scan structured strings, not JSON escapes of a credential.
        for text in self._strings(value):
            if any(secret in text for secret in self._secrets):
                self._reject()

    def check_generated(self, value: object) -> None:
        self.check_credentials(value)
        for text in self._strings(value):
            surfaces = [text, text[::-1]]
            # These checks cover exact common encodings, not arbitrary semantic
            # transformations. Inputs are bounded before this method is called.
            for token in re.findall(r"[A-Za-z0-9+/]{64,}={0,2}", text):
                if len(token) <= 65536:
                    try:
                        surfaces.append(base64.b64decode(token, validate=True).decode("utf-8"))
                    except (ValueError, UnicodeError):
                        pass
            for token in re.findall(r"\b[0-9a-fA-F]{64,}\b", text):
                if len(token) <= 65536:
                    try:
                        surfaces.append(bytes.fromhex(token).decode("utf-8"))
                    except (ValueError, UnicodeError):
                        pass
            for surface in surfaces:
                if any(secret in surface for secret in self._secrets):
                    self._reject()
                normalized = _words(surface)
                if any(fragment in normalized for fragment in _protected_fragments()):
                    self._reject()

    def check_proposal(self, proposal) -> None:
        self.check_credentials(proposal.model_dump(mode="json"))
        self.check_generated({
            "message": proposal.message,
            "action": proposal.action.model_dump(mode="json") if proposal.action else None,
            "draft": proposal.context.draft.model_dump(mode="json") if proposal.context.draft else None,
        })

    @staticmethod
    def _strings(value):
        if isinstance(value, str):
            yield value
        elif isinstance(value, dict):
            for key, item in value.items():
                yield str(key)
                yield from DisclosurePolicy._strings(item)
        elif isinstance(value, (list, tuple)):
            for item in value:
                yield from DisclosurePolicy._strings(item)


def asks_for_private_information(text: str) -> bool:
    """A narrow chat refusal rule; ordinary security discussion remains valid."""
    value = _words(text)
    objects = r"(?:system prompt|system instructions|hidden instructions|api key|api_key|environment variables|системн\w* пром[п]?т|системн\w* инструкц|скрыт\w* инструкц|переменн\w* окружен|секретн\w* ключ|данн\w* других пользовател|чуж\w* (?:данн|задач|встреч))"
    verbs = r"(?:reveal|print|show|repeat|translate|encode|вывед|покаж|раскро|повтор|перевед|закодир|прочит)\w*"
    return bool(re.search(verbs + r".{0,160}" + objects, value)
                or re.search(objects + r".{0,100}" + verbs, value)
                or re.search(r"(?:repeat|print|show) (?:everything|all text) (?:above|before)", value))
