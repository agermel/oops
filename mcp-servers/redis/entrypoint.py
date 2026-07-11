import os
import sys

import redis.utils

redis.utils.DEFAULT_RESP_VERSION = 2

args = ["redis-mcp-server"]
for env_var, flag in [
    ("REDIS_HOST", "--host"),
    ("REDIS_PORT", "--port"),
    ("REDIS_DB", "--db"),
    ("REDIS_USERNAME", "--username"),
    ("REDIS_PWD", "--password"),
]:
    value = os.environ.get(env_var)
    if value:
        args.extend([flag, value])

args.extend(sys.argv[1:])
sys.argv = args

from src.main import cli

cli()
