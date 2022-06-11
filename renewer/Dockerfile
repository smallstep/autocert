FROM smallstep/step-cli:0.17.2

USER root
ENV CRT="/var/run/autocert.step.sm/site.crt"
ENV KEY="/var/run/autocert.step.sm/site.key"
ENV STEP_ROOT="/var/run/autocert.step.sm/root.crt"

COPY renewer/renewer.sh /home/step/
RUN chmod +x /home/step/renewer.sh
CMD ["/home/step/renewer.sh"]
