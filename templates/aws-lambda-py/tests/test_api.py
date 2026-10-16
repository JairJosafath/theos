import base64
import json
from typing import TYPE_CHECKING

import pytest

from app.api import PUT_ITEM, HttpApiEvent, Log, answer, route

if TYPE_CHECKING:
    from collections.abc import Mapping

    from app.item_store import ItemStore
    from app.items import HttpResponse, Item


class MemoryStore:
    """An ItemStore in memory, which fails every call when told to."""

    def __init__(self, *, failing: bool = False) -> None:
        self.items: dict[str, Item] = {}
        self.failing = failing

    def get(self, item_id: str) -> Item | None:
        if self.failing:
            msg = "timeout"
            raise TimeoutError(msg)
        return self.items.get(item_id)

    def put(self, item: Item) -> None:
        if self.failing:
            msg = "timeout"
            raise TimeoutError(msg)
        self.items[item.id] = item


def request(
    route_key: str, item_id: str | None, body: str | None = None, *, base64_encoded: bool = False
) -> HttpApiEvent:
    path = route_key.split(" ")[1].replace("{id}", item_id or "")
    event: HttpApiEvent = {
        "routeKey": route_key,
        "rawPath": path,
        "headers": {},
        "requestContext": {"requestId": "request-1"},
    }
    if item_id is not None:
        event["pathParameters"] = {"id": item_id}
    if body is not None:
        event["body"] = body
        event["isBase64Encoded"] = base64_encoded
    return event


def no_log(_message: str, _attributes: Mapping[str, object]) -> None:
    pass


def send(event: HttpApiEvent, store: ItemStore, log: Log = no_log) -> HttpResponse:
    """Answer `event` from `store`, as the handler does."""
    return answer(event, lambda request: route(request, store), log)


def test_puts_an_item_then_gets_it() -> None:
    store = MemoryStore()

    put = send(request(PUT_ITEM, "i1", '{"name":"Lamp","quantity":3}'), store)
    got = send(request("GET /items/{id}", "i1"), store)

    assert put["statusCode"] == 200
    assert got == {
        "statusCode": 200,
        "headers": {"content-type": "application/json"},
        "body": '{"id":"i1","name":"Lamp","quantity":3}',
    }


def test_reads_a_base64_encoded_body() -> None:
    body = base64.b64encode('{"name":"Zoë","quantity":1}'.encode()).decode()

    put = send(request(PUT_ITEM, "i1", body, base64_encoded=True), MemoryStore())

    assert put["body"] == '{"id":"i1","name":"Zoë","quantity":1}'


@pytest.mark.parametrize(
    ("event", "status"),
    [
        (request("GET /items/{id}", "nope"), 404),
        (request("GET /items/{id}", "a b"), 400),
        (request("GET /items/{id}", None), 400),
        (request(PUT_ITEM, "i1", '{"name":""}'), 400),
        (request(PUT_ITEM, "i1", "not base64!", base64_encoded=True), 400),
        (request("DELETE /items/{id}", "i1"), 404),
    ],
    ids=["missing item", "bad id", "no id", "bad body", "bad base64", "unknown route"],
)
def test_answers_a_status_and_a_message(event: HttpApiEvent, status: int) -> None:
    response = send(event, MemoryStore())

    assert response["statusCode"] == status
    assert "message" in json.loads(response["body"])


def test_answers_500_without_details_when_the_store_fails_and_logs_why() -> None:
    logged: list[tuple[str, Mapping[str, object]]] = []

    response = send(
        request("GET /items/{id}", "i1"),
        MemoryStore(failing=True),
        lambda message, attributes: logged.append((message, attributes)),
    )

    assert response["statusCode"] == 500
    assert "timeout" not in response["body"]
    assert logged == [
        (
            "The request failed.",
            {"requestId": "request-1", "routeKey": "GET /items/{id}", "error": "timeout"},
        )
    ]
