FROM debian:jessie

MAINTAINER neowisard <kutuzovnick@gmail.com>

# add our user and group first to make sure their IDs get assigned consistently, regardless of whatever dependencies get added
RUN groupadd -r jasmin && useradd -r -g jasmin jasmin

ENV JASMIN_VERSION 0.9b3

# Install requirements
RUN apt-get update && apt-get install -y \
    python2.7 \
    python-pip \
    python-dev \
    libffi-dev \
    libssl-dev \
  && apt-get clean \
  && rm -rf /var/lib/apt/lists/*

# Install Jasmin SMS gateway
RUN mkdir -p /var/log/jasmin \
  && chown jasmin:jasmin /var/log/jasmin \
  && pip install xlrd \
  && pip install redis \
  && pip install --upgrade pyasn1 \
  && pip install --upgrade twisted

EXPOSE 2775 8990
VOLUME ["/var/log/jasmin","/etc/jasmin","/usr/local/lib/python2.7/dist-packages/jasmin"]
ADD jasmin.tar.gz /usr/
RUN cd /usr/jasmin && python setup.py install

COPY docker-entrypoint.sh /
ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["jasmind.py","-u","neo","-p","neo","--enable-interceptor-client","--disable-http-api","--disable-deliver-thrower"]