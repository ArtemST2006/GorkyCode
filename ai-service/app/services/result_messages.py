"""Trusted narration templates for backend results.

Backend stores candidate narration in conversation history. Consequently no free
text from records, change values, or the user may be interpolated here. Only list
lengths, checked integer selection numbers, and fixed field labels are displayed.
"""

from app.schemas.respond import RespondRequest


def _selection_instruction(candidates: list[dict]) -> str:
    # Never renumber candidates or print arbitrary values from the open data
    # object. Fall back to the interface for malformed or very long number lists.
    numbers = []
    length = 0
    for candidate in candidates:
        number = candidate.get("number")
        if type(number) is not int or number <= 0 or number.bit_length() > 128:
            break
        rendered = str(number)
        length += len(rendered) + 2
        if length > 4500:
            break
        numbers.append(rendered)
    else:
        return "Выберите номера для подтверждения: " + ", ".join(numbers) + "."
    return "Выберите исходные номера записей из списка в интерфейсе для подтверждения."


def result_message(payload: RespondRequest) -> str:
    """Use the validated operation and full lists, without modifying data."""
    data = payload.data
    if payload.status == "ok":
        if payload.intent == "create_event":
            return "Запись создана."
        count = len(data["items"])
        if not count:
            return "Записей не найдено. Уточните запрос."
        return f"Найдено записей: {count}."

    if payload.status in ("candidates", "update_candidates"):
        candidates = data["candidates"]
        if not candidates:
            return "Подходящих записей не найдено. Уточните запрос."
        operation = "Удаление" if payload.status == "candidates" else "Изменение"
        message = f"Найдено записей: {len(candidates)}. {operation} ещё не выполнено."
        if payload.status == "update_candidates":
            labels = [
                label for key, label in (
                    ("title", "название"), ("date", "дата"),
                    ("time", "время"), ("status", "статус"),
                )
                if key in data["changes"]
            ]
            if labels:
                message += " Предлагается изменить: " + ", ".join(labels) + "."
        return message + " " + _selection_instruction(candidates)

    if payload.status == "deleted":
        return f"Удалено записей: {len(data['deleted_events'])}."
    # RespondRequest enforces the remaining supported variant: on_update/updated.
    return f"Обновлено записей: {len(data['updated_events'])}."
