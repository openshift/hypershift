FROM registry.fedoraproject.org/fedora-minimal:41

RUN dnf install -y python-pip \
  && dnf clean all \
  && useradd docs

WORKDIR /home/docs
USER docs

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt \
  && mkdir /home/docs/hypershift

WORKDIR /home/docs/hypershift
ENTRYPOINT [ "/home/docs/.local/bin/mkdocs" ]
