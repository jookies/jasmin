FROM debian:jessie

MAINTAINER Jookies LTD <jasmin@jookies.net>

# add our user and group first to make sure their IDs get assigned consistently, regardless of whatever dependencies get added
RUN groupadd -r jasmin && useradd -r -g jasmin jasmin

ENV JASMIN_VERSION 0.9b7

# Install requirements
RUN apt-get update && apt-get install -y \
    python2.7 \
    python-pip \
    python-dev \
    libffi-dev \
    libssl-dev \
    rabbitmq-server \
    redis-server \
  && apt-get clean \
  && rm -rf /var/lib/apt/lists/*

# Install Jasmin SMS gateway
RUN mkdir -p /etc/jasmin/resource \
    /etc/jasmin/store \
    /var/log/jasmin \
  && chown jasmin:jasmin /etc/jasmin/store \
    /var/log/jasmin \
  && pip install --pre jasmin=="$JASMIN_VERSION"

# Change binding host for jcli
RUN sed -i '/\[jcli\]/a bind=0.0.0.0' /etc/jasmin/jasmin.cfg

EXPOSE 2775 8990 1401
VOLUME ["/var/log/jasmin", "/etc/jasmin", "/etc/jasmin/store"]

COPY docker-entrypoint.sh /
ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["jasmind.py", "--enable-interceptor-client", "-u", "jcliadmin", "-p", "jclipwd"]
