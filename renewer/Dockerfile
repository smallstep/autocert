FROM smallstep/step-cli:0.26.0

USER root
ENV CRT="/var/run/autocert.step.sm/site.crt"
ENV KEY="/var/run/autocert.step.sm/site.key"
ENV P12="/var/run/autocert.step.sm/site.p12"
ENV PEM="/var/run/autocert.step.sm/site.pem"
ENV STEP_ROOT="/var/run/autocert.step.sm/root.crt"

COPY renewer/renewerexec.sh /home/step/
RUN chmod +x /home/step/renewerexec.sh

ENTRYPOINT ["/bin/bash", "-c", "step ca renew --daemon --exec /home/step/renewerexec.sh $CRT $KEY"]
