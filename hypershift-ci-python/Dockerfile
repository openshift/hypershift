FROM registry.access.redhat.com/ubi9/python-312:latest

COPY requirements.txt ${APP_ROOT}
# Install the dependencies
RUN pip install -r ${APP_ROOT}/requirements.txt