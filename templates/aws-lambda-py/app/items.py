"""The API's rules, free of AWS.

What an item is, which ids and bodies are valid, and the responses the handler
returns. Nothing here imports boto3, so the tests need neither it nor AWS.
"""

import json
import re
from dataclasses import asdict, dataclass
from decimal import Decimal
from typing import TypedDict

# Ids end up in URLs and DynamoDB keys: a short, URL-safe alphabet.
_ID_PATTERN = re.compile(r"[A-Za-z0-9_-]{1,64}")
# A name is for people to read; DynamoDB items can be 400 KB, names can't.
MAX_NAME_LENGTH = 200
# The largest whole number every JSON client reads exactly (JavaScript's
# Number.MAX_SAFE_INTEGER); DynamoDB itself takes at most 38 digits.
MAX_QUANTITY = 2**53 - 1


@dataclass(frozen=True)
class Item:
    """An item as the API stores and returns it."""

    id: str
    name: str
    quantity: int


class InvalidInputError(ValueError):
    """Untrusted input the API refuses; the message says why, for the client."""


class HttpResponse(TypedDict):
    """An HTTP response, in the shape API Gateway's HTTP API reads from a Lambda."""

    statusCode: int
    headers: dict[str, str]
    body: str


def check_id(raw: str | None) -> str:
    """Return an id from the path, or raise InvalidInputError."""
    if raw is None or _ID_PATTERN.fullmatch(raw) is None:
        msg = 'The id must be 1 to 64 letters, digits, "-" or "_".'
        raise InvalidInputError(msg)
    return raw


def check_item(item_id: str, body: str | None) -> Item:
    """Return the item a request body describes, or raise InvalidInputError.

    The body must be a JSON object with a name and a quantity.
    """
    try:
        parsed: object = json.loads(body or "")
    # ValueError covers JSONDecodeError and a number too long to convert.
    except ValueError:
        msg = "The body must be JSON."
        raise InvalidInputError(msg) from None
    return check_fields(item_id, parsed)


def check_fields(item_id: str, value: object) -> Item:
    """Return the item `value` holds, or raise InvalidInputError.

    The store calls it too, on what DynamoDB returns, so a row written by
    something else can't reach a client unchecked. DynamoDB returns numbers as
    Decimal, so a whole Decimal is a quantity too.
    """
    if not isinstance(value, dict):
        msg = "The body must be a JSON object."
        raise InvalidInputError(msg)
    name: object = value.get("name")
    quantity: object = value.get("quantity")
    if not isinstance(name, str) or not name.strip() or len(name) > MAX_NAME_LENGTH:
        msg = f'"name" must be a non-empty string of at most {MAX_NAME_LENGTH} characters.'
        raise InvalidInputError(msg)
    whole = _whole_number(quantity)
    if whole is None or not 0 <= whole <= MAX_QUANTITY:
        msg = f'"quantity" must be a whole number from 0 to {MAX_QUANTITY}.'
        raise InvalidInputError(msg)
    return Item(id=item_id, name=name.strip(), quantity=whole)


def _whole_number(value: object) -> int | None:
    # bool is an int in Python, but true is not a quantity.
    if isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, Decimal) and value.is_finite() and value == value.to_integral_value():
        return int(value)
    return None


def json_response(status_code: int, value: object) -> HttpResponse:
    """Return a JSON response; an Item is written as its fields."""
    if isinstance(value, Item):
        value = asdict(value)
    return {
        "statusCode": status_code,
        "headers": {"content-type": "application/json"},
        "body": json.dumps(value, ensure_ascii=False, separators=(",", ":")),
    }


def problem(status_code: int, message: str) -> HttpResponse:
    """Return an error response a client can read: the status and one message."""
    return json_response(status_code, {"message": message})
