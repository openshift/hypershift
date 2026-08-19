FROM ocp_builder_rhel-9-base-nodejs-openshift-4.22

USER root

# Chromium system dependencies required by Playwright on RHEL9
RUN dnf install -y \
    alsa-lib atk at-spi2-atk cups-libs libdrm libXcomposite \
    libXdamage libXrandr mesa-libgbm pango nss nss-util nspr \
    libxkbcommon libXfixes libXcursor libXext libXi libXtst \
    libwayland-client wget tar && \
    dnf clean all

# Install oc CLI
RUN wget -q https://mirror.openshift.com/pub/openshift-v4/clients/oc/latest/linux/oc.tar.gz && \
    tar xzf oc.tar.gz -C /usr/local/bin oc && \
    rm oc.tar.gz

ENV PLAYWRIGHT_BROWSERS_PATH=/opt/playwright

WORKDIR /hypershift/test/ui

COPY package.json package-lock.json ./
RUN npm ci

COPY . ./

RUN npx playwright install chromium

CMD ["npm", "test"]
