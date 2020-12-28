FROM smallstep/step-cli:0.15.3

USER root
ENV USER_KEY="/var/run/autocert.step.sm/ssh_user_key"
ENV HOST_KEY="/var/run/autocert.step.sm/ssh_host_key"
ENV STEP_ROOT="/var/run/autocert.step.sm/root.crt"
ENV RENEWAL_SEC="21600"
ENV RENEWAL_JITTER_SEC="600"

COPY renewer/ssh/renewer.sh /home/step/
RUN chmod +x /home/step/renewer.sh
CMD ["/home/step/renewer.sh"]
