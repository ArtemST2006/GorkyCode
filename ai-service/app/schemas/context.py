"""Empty, stored, clarification and completed conversation contexts."""

from typing import Union

from app.schemas.base import ContractModel
from app.schemas.create_event import CreateEventDraft
from app.schemas.delete_event import DeleteCandidateContext, DeleteSearchDraft
from app.schemas.list_events import ListEventsDraft
from app.schemas.messages import RecentMessages
from app.schemas.search_events import SearchEventsDraft
from app.schemas.update_event import UpdateCandidateContext, UpdateSearchDraft


class EmptyContext(ContractModel):
    pass


class StoredContext(ContractModel):
    recent_messages: RecentMessages
    draft: Union[CreateEventDraft, ListEventsDraft, SearchEventsDraft, DeleteSearchDraft, UpdateSearchDraft]


JobRequestContext = Union[EmptyContext, DeleteCandidateContext, UpdateCandidateContext, StoredContext]


class CreateEventContext(ContractModel):
    recent_messages: RecentMessages
    draft: CreateEventDraft


class ListEventsContext(ContractModel):
    recent_messages: RecentMessages
    draft: ListEventsDraft


class SearchEventsContext(ContractModel):
    recent_messages: RecentMessages
    draft: SearchEventsDraft


class ReadyContext(ContractModel):
    recent_messages: RecentMessages
    draft: None
