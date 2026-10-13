"""The Lambda entry point the stack names: wiring only, the work is in answer."""

import json
import os
import sys
from typing import TYPE_CHECKING

import boto3

from app.api import HttpApiEvent, answer
from app.item_store import DynamoDbItemStore

if TYPE_CHECKING:
    from collections.abc import Mapping

    from app.items import HttpResponse

# Created once per execution environment, outside the handler, so warm
# invocations reuse the client and its connections.
_TABLE_NAME = os.environ.get("TABLE_NAME", "")
if not _TABLE_NAME:
    msg = "TABLE_NAME is not set; the stack sets it to the table the function uses."
    raise RuntimeError(msg)
_STORE = DynamoDbItemStore(boto3.resource("dynamodb").Table(_TABLE_NAME))


def _log(entry: Mapping[str, object]) -> None:
    # Lambda sends what the function writes to CloudWatch Logs, one entry a line.
    sys.stderr.write(json.dumps(entry) + "\n")


def handler(event: HttpApiEvent, _context: object) -> HttpResponse:
    """Answer one API Gateway request."""
    return answer(event, _STORE, _log)
