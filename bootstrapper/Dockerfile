FROM smallstep/step-cli:0.15.0

USER root
ENV CRT="/var/run/autocert.step.sm/site.crt"
ENV KEY="/var/run/autocert.step.sm/site.key"
ENV STEP_ROOT="/var/run/autocert.step.sm/root.crt"

COPY bootstrapper/bootstrapper.sh /home/step/
RUN chmod +x /home/step/bootstrapper.sh
CMD ["/home/step/bootstrapper.sh"]
