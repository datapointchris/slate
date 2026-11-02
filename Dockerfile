# syntax=docker/dockerfile:1

FROM ghcr.io/astral-sh/uv:python3.14-bookworm-slim AS builder

ENV UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    UV_PYTHON_DOWNLOADS=0

WORKDIR /app

# Dependencies first, in a layer of their own, so a source change reuses it.
RUN --mount=type=cache,target=/root/.cache/uv \
    --mount=type=bind,source=uv.lock,target=uv.lock \
    --mount=type=bind,source=pyproject.toml,target=pyproject.toml \
    uv sync --locked --no-install-project --no-dev

COPY pyproject.toml uv.lock README.md ./
COPY src ./src
# Non-editable, so the package lands in the venv and the runtime stage needs no source tree.
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --locked --no-dev --no-editable


FROM python:3.14-slim-bookworm

LABEL org.opencontainers.image.source="https://github.com/datapointchris/slate" \
      org.opencontainers.image.description="slate's API: a work tracker built for coding agents" \
      org.opencontainers.image.licenses="MIT"

# A uid of its own, so files it writes to a mounted volume read as slate's and never as a person's.
RUN groupadd --system --gid 10400 slate && \
    useradd --system --uid 10400 --gid slate --no-create-home --shell /usr/sbin/nologin slate

WORKDIR /app
# Owned by root, so the process cannot rewrite its own code, and the root filesystem can mount read-only.
COPY --from=builder /app/.venv /app/.venv
COPY alembic.ini ./

ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

USER 10400:10400
EXPOSE 8000

# Migrations run as their own step before a rollout: `alembic upgrade head` in this image.
CMD ["uvicorn", "--factory", "slate.api.main:create_app", "--host", "0.0.0.0", "--port", "8000", "--no-access-log", "--timeout-graceful-shutdown", "10"]
