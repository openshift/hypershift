# Use multi-arch Python Alpine base image
FROM python:3.11-alpine

# Install git and create user
RUN apk add --no-cache git \
  && adduser -D -s /bin/sh docs

WORKDIR /home/docs
USER docs

# Copy requirements and install Python dependencies
COPY --chown=docs:docs requirements.txt .
RUN pip install --user --no-cache-dir -r requirements.txt \
  && mkdir -p /home/docs/hypershift

WORKDIR /home/docs/hypershift

# Set PATH to include user local bin and configure git for container use
ENV PATH="/home/docs/.local/bin:${PATH}"
RUN git config --global --add safe.directory /home/docs/hypershift

ENTRYPOINT [ "mkdocs" ]
