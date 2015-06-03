FROM ubuntu:trusty

MAINTAINER Fourat ZOUARI <fourat@gmail.com>

RUN apt-get update

RUN apt-get install -y curl
RUN curl -s https://packagecloud.io/install/repositories/jookies/python-jasmin/script.deb.sh | sudo bash

RUN apt-get update && apt-get install -y python-jasmin

EXPOSE 2775 8990 1401