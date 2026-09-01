FROM registry.access.redhat.com/ubi9/python-312:latest

# Install uv
RUN pip install --no-cache-dir uv==0.12.7

# Copy project definition and lock file
COPY pyproject.toml uv.lock ${APP_ROOT}/

# Install locked dependencies into a uv-managed venv
RUN cd ${APP_ROOT} && uv sync --frozen --no-dev

# Add the venv to the PYTHONPATH for the global interpreter
# The user can call a Python script in any of the following ways:
# "uv run --frozen script.py"
# "python3 script.py"
# "script.py"
ENV PYTHONPATH="${APP_ROOT}/.venv/lib/python3.12/site-packages"
