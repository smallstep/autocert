FROM smallstep/step-cli:0.15.3

USER root
ENV USER_KEY="/var/run/autocert.step.sm/ssh_user_key"
ENV USER_CA="/var/run/autocert.step.sm/ssh_user_ca.pub"
ENV HOST_KEY="/var/run/autocert.step.sm/ssh_host_key"
ENV HOST_CA="/var/run/autocert.step.sm/ssh_host_ca.pub"

COPY bootstrapper/ssh/bootstrapper.sh /home/step/
RUN chmod +x /home/step/bootstrapper.sh
CMD ["/home/step/bootstrapper.sh"]
