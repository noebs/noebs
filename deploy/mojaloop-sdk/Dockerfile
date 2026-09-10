FROM alpine/git:2.49.1 AS source
WORKDIR /source
RUN git init && git remote add origin https://github.com/mojaloop/sdk-scheme-adapter.git \
    && git fetch --depth=1 origin 6594dc5689a95dffece23d481a407e4368d80a96 \
    && git checkout --detach FETCH_HEAD
COPY 0001-noebs-immutable-prepare.patch /tmp/profile.patch
RUN git apply --check /tmp/profile.patch && git apply /tmp/profile.patch

FROM mojaloop/sdk-scheme-adapter@sha256:4d3b0ca0f2fd1dd8ea15957de38718de9cb4c64a4ecb919d1b3086c5da5ce788
COPY --from=source --chown=ml-user /source/modules/api-svc/src/lib/model/InboundTransfersModel.js /opt/app/modules/api-svc/src/lib/model/InboundTransfersModel.js
COPY --from=source --chown=ml-user /source/modules/api-svc/src/lib/model/OutboundTransfersModel.js /opt/app/modules/api-svc/src/lib/model/OutboundTransfersModel.js
COPY --from=source --chown=ml-user /source/modules/api-svc/src/lib/model/TransfersModel.js /opt/app/modules/api-svc/src/lib/model/TransfersModel.js
COPY --from=source --chown=ml-user /source/modules/api-svc/src/InboundServer/handlers.js /opt/app/modules/api-svc/src/InboundServer/handlers.js
COPY --from=source --chown=ml-user /source/modules/api-svc/src/OutboundServer/index.js /opt/app/modules/api-svc/src/OutboundServer/index.js
COPY --chown=ml-user noebs-profile.js /opt/app/modules/api-svc/src/lib/model/lib/noebs-profile.js
LABEL io.noebs.sdk.upstream="6594dc5689a95dffece23d481a407e4368d80a96" io.noebs.sdk.profile="sdg-msisdn-v1"
CMD ["yarn", "run", "start:api-svc"]
