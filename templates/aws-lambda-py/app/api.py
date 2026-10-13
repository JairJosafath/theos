"""Answers the HTTP API's requests: routes them, checks input, picks the status."""

import base64
import binascii
from collections.abc import Callable, Mapping
from typing import TYPE_CHECKING, NotRequired, TypedDict

from app.items import HttpResponse, InvalidInputError, check_id, check_item, json_response, problem

if TYPE_CHECKING:
    from app.item_store import ItemStore

type Log = Callable[[Mapping[str, object]], None]
"""Where the API writes its structured logs: one JSON object per entry."""


class RequestContext(TypedDict):
    """The part of an event's requestContext the API reads."""

    requestId: str


class HttpApiEvent(TypedDict):
    """The fields of an HTTP API (payload 2.0) event the API reads."""

    routeKey: str
    requestContext: RequestContext
    pathParameters: NotRequired[dict[str, str]]
    body: NotRequired[str]
    isBase64Encoded: NotRequired[bool]


def answer(event: HttpApiEvent, store: ItemStore, log: Log) -> HttpResponse:
    """Answer one request of the HTTP API.

    Every route returns a status code a client can act on; an unexpected
    failure is logged and answered with 500, without its details.
    """
    try:
        return _route(event, store)
    except InvalidInputError as error:
        return problem(400, str(error))
    except Exception as error:  # noqa: BLE001 - every failure is answered, and logged
        log(
            {
                "level": "ERROR",
                "message": "The request failed.",
                "requestId": event["requestContext"]["requestId"],
                "routeKey": event["routeKey"],
                "error": str(error),
            }
        )
        return problem(500, "Something went wrong. Try again later.")


def _route(event: HttpApiEvent, store: ItemStore) -> HttpResponse:
    item_id = check_id(event.get("pathParameters", {}).get("id"))
    match event["routeKey"]:
        case "GET /items/{id}":
            item = store.get(item_id)
            if item is None:
                return problem(404, f"No item {item_id}.")
            return json_response(200, item)
        case "PUT /items/{id}":
            item = check_item(item_id, _body(event))
            # A put replaces the whole item, so repeating a request is harmless.
            store.put(item)
            return json_response(200, item)
        case other:
            return problem(404, f"No route {other}.")


def _body(event: HttpApiEvent) -> str | None:
    body = event.get("body")
    if body is None or not event.get("isBase64Encoded", False):
        return body
    try:
        return base64.b64decode(body, validate=True).decode()
    except binascii.Error, UnicodeDecodeError:
        msg = "The body must be JSON."
        raise InvalidInputError(msg) from None
