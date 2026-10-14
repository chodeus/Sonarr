# syntax=docker/dockerfile:1
# check=skip=InvalidDefaultArgInFrom
ARG UPSTREAM_IMAGE
ARG UPSTREAM_TAG_SHA

FROM ${UPSTREAM_IMAGE}:${UPSTREAM_TAG_SHA}
EXPOSE 8989
ARG IMAGE_STATS
ENV IMAGE_STATS=${IMAGE_STATS} WEBUI_PORTS="8989/tcp"

RUN apk add --no-cache libintl sqlite-libs icu-libs

ARG VERSION
ARG VERSION_BRANCH
ARG PACKAGE_VERSION=${VERSION}

# sonarr.tar.gz comes from ./build.sh compile.
RUN --mount=type=bind,source=sonarr.tar.gz,target=/tmp/sonarr.tar.gz \
    mkdir "${APP_DIR}/bin" && \
    tar xzf /tmp/sonarr.tar.gz -C "${APP_DIR}/bin" --strip-components=1 && \
    rm -rf "${APP_DIR}/bin/Sonarr.Update" && \
    echo -e "PackageVersion=${PACKAGE_VERSION}\nPackageAuthor=[chodeus](https://github.com/chodeus)\nUpdateMethod=Docker\nBranch=${VERSION_BRANCH}" > "${APP_DIR}/package_info" && \
    chmod -R u=rwX,go=rX "${APP_DIR}" && \
    chmod +x "${APP_DIR}/bin/Sonarr" "${APP_DIR}/bin/ffprobe"

COPY root/ /
RUN find /etc/s6-overlay/s6-rc.d -name "run*" -execdir chmod +x {} +
