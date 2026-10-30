"""Fixed clarification questions; never publish extractor free text."""

def clarification_message(decision) -> str:
    """Questions contain no free text from the privileged extractor."""
    draft = decision.draft
    if decision.intent == "create_event":
        if not getattr(draft, "record_type", None):
            return "Что создать: задачу, встречу или напоминание?"
        if not getattr(draft, "title", None):
            return "Как назвать запись?"
        if not getattr(draft, "date", None):
            return "На какую дату создать запись?"
        if draft.record_type in ("meeting", "reminder") and not getattr(draft, "time", None):
            return "На какое время создать запись?"
        return "Уточните дату, время или содержание записи."
    return {
        "list_events": "Уточните, какие записи показать.",
        "search_events": "Уточните тему или условия поиска.",
        "on_update": "Уточните, какую запись и что в ней нужно изменить.",
        "on_delete": "Уточните, какие записи нужно удалить.",
    }.get(decision.intent, "Уточните календарный запрос.")
