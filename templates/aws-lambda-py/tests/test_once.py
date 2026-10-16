from typing import TYPE_CHECKING, override

import pytest
from aws_lambda_powertools.utilities.idempotency import BasePersistenceLayer
from aws_lambda_powertools.utilities.idempotency.exceptions import (
    IdempotencyAlreadyInProgressError,
    IdempotencyItemAlreadyExistsError,
    IdempotencyItemNotFoundError,
    IdempotencyValidationError,
)
from aws_lambda_powertools.utilities.typing import LambdaContext

from app.api import PUT_ITEM, HttpApiEvent, answer, route
from app.items import json_response, problem
from app.once import (
    RefusedError,
    answering_once,
    idempotency_config,
    keeping_only_success,
    once_per_key,
)

if TYPE_CHECKING:
    from collections.abc import Mapping

    from aws_lambda_powertools.utilities.idempotency.persistence.datarecord import DataRecord

    from app.api import Route
    from app.items import HttpResponse, Item

LAMP = '{"name":"Lamp","quantity":3}'


def put(item_id: str, body: str, key: str | None = "k1") -> HttpApiEvent:
    return {
        "routeKey": PUT_ITEM,
        "rawPath": f"/items/{item_id}",
        "headers": {} if key is None else {"idempotency-key": key},
        "requestContext": {"requestId": "request-1"},
        "pathParameters": {"id": item_id},
        "body": body,
    }


class MemoryStore:
    """An ItemStore in memory that counts its writes, and fails them when told to."""

    def __init__(self) -> None:
        self.items: dict[str, Item] = {}
        self.writes = 0
        self.failing = False

    def get(self, item_id: str) -> Item | None:
        return self.items.get(item_id)

    def put(self, item: Item) -> None:
        if self.failing:
            msg = "timeout"
            raise TimeoutError(msg)
        self.writes += 1
        self.items[item.id] = item


class MemoryRecords(BasePersistenceLayer):
    """Powertools' idempotency records in a dict, where the stack keeps them in DynamoDB."""

    def __init__(self) -> None:
        super().__init__()  # type: ignore[no-untyped-call] # Powertools leaves it unannotated.
        self.records: dict[str, DataRecord] = {}

    @override
    def _get_record(self, idempotency_key: str) -> DataRecord:
        record = self.records.get(idempotency_key)
        if record is None:
            raise IdempotencyItemNotFoundError
        return record

    @override
    def _put_record(self, data_record: DataRecord) -> None:
        # As the table's condition does: a key is written once, until its record goes.
        if data_record.idempotency_key in self.records:
            raise IdempotencyItemAlreadyExistsError
        self.records[data_record.idempotency_key] = data_record

    @override
    def _update_record(self, data_record: DataRecord) -> None:
        self.records[data_record.idempotency_key] = data_record

    @override
    def _delete_record(self, data_record: DataRecord) -> None:
        del self.records[data_record.idempotency_key]


class Context(LambdaContext):
    """The part of Lambda's context Idempotency reads: the time the invocation has left."""

    @override
    @staticmethod
    def get_remaining_time_in_millis() -> int:
        return 10_000


def no_log(_message: str, _attributes: Mapping[str, object]) -> None:
    pass


class Api:
    """The PUT route as the handler wires it, its store and records in memory."""

    def __init__(self, work: Route | None = None) -> None:
        self.store = MemoryStore()
        self.records = MemoryRecords()
        config = idempotency_config()
        config.register_lambda_context(Context())
        self.put_once = once_per_key(
            work or (lambda event: route(event, self.store)), self.records, config
        )

    def send(self, event: HttpApiEvent) -> HttpResponse:
        return answer(event, self.put_once, no_log)


def test_a_put_sent_again_with_the_same_key_gets_the_first_answer_and_writes_once() -> None:
    api = Api()

    first = api.send(put("i1", LAMP))
    again = api.send(put("i1", LAMP))

    assert first["statusCode"] == 200
    assert again == first
    assert api.store.writes == 1


@pytest.mark.filterwarnings("ignore:No idempotency key value found")
def test_a_put_without_the_key_is_simply_run() -> None:
    api = Api()

    first = api.send(put("i1", LAMP, key=None))
    again = api.send(put("i1", LAMP, key=None))

    assert first["statusCode"] == again["statusCode"] == 200
    assert api.store.writes == 2
    assert api.records.records == {}


def test_a_refusal_keeps_no_record_so_a_corrected_retry_runs() -> None:
    api = Api()

    refused = api.send(put("i1", '{"name":""}'))
    corrected = api.send(put("i1", LAMP))

    assert refused["statusCode"] == 400
    assert corrected["statusCode"] == 200
    assert api.store.writes == 1


def test_a_failure_keeps_no_record_so_a_retry_runs() -> None:
    api = Api()
    api.store.failing = True

    failed = api.send(put("i1", LAMP))
    api.store.failing = False
    retried = api.send(put("i1", LAMP))

    assert failed["statusCode"] == 500
    assert retried["statusCode"] == 200
    assert api.store.writes == 1


@pytest.mark.parametrize(
    "other",
    [put("i1", '{"name":"Desk","quantity":1}'), put("i2", LAMP)],
    ids=["another body", "another path"],
)
def test_the_key_sent_with_another_request_is_answered_422(other: HttpApiEvent) -> None:
    api = Api()

    api.send(put("i1", LAMP))
    response = api.send(other)

    assert response == problem(422, "This Idempotency-Key was sent with another request.")
    assert api.store.writes == 1


def test_a_retry_while_the_first_still_runs_is_answered_409() -> None:
    retries: list[HttpResponse] = []

    def first(event: HttpApiEvent) -> HttpResponse:
        # The same request arrives again before the first is answered.
        retries.append(api.send(event))
        return route(event, api.store)

    api = Api(first)
    answered = api.send(put("i1", LAMP))

    assert answered["statusCode"] == 200
    assert retries == [problem(409, "The same request is still running. Try again shortly.")]
    assert api.store.writes == 1


def answers(response: HttpResponse) -> Route:
    return lambda _event: response


def raises(error: Exception) -> Route:
    def run(_event: HttpApiEvent) -> HttpResponse:
        raise error

    return run


EVENT = put("i1", LAMP)


def test_keeping_only_success_passes_an_answer_that_changed_something_through() -> None:
    ok = json_response(200, {"id": "i1"})

    assert keeping_only_success(answers(ok))(EVENT) is ok


def test_keeping_only_success_raises_a_refusal_so_no_record_of_it_is_kept() -> None:
    refusal = problem(400, "The body must be JSON.")

    with pytest.raises(RefusedError) as raised:
        keeping_only_success(answers(refusal))(EVENT)

    assert raised.value.response is refusal


def test_keeping_only_success_passes_a_server_error_through() -> None:
    failure = problem(500, "Something went wrong. Try again later.")

    assert keeping_only_success(answers(failure))(EVENT) is failure


@pytest.mark.parametrize(
    ("error", "status"),
    [
        (RefusedError(problem(404, "No item i1.")), 404),
        (IdempotencyAlreadyInProgressError("key"), 409),
        (IdempotencyValidationError("key"), 422),
    ],
    ids=["a refusal", "still running", "another request"],
)
def test_answering_once_answers_what_a_client_can_act_on(error: Exception, status: int) -> None:
    assert answering_once(raises(error))(EVENT)["statusCode"] == status


def test_answering_once_raises_anything_else_on_for_answer_to_log() -> None:
    with pytest.raises(TimeoutError, match="timeout"):
        answering_once(raises(TimeoutError("timeout")))(EVENT)
