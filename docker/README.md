# What is Jasmin?

Jasmin is a very complete open source SMS Gateway with many enterprise-class features such as:

* SMPP Client / Server
* HTTP Client / Server
* Console-based configuration, no service restart required
* Based on AMQP broker for store&forward mechanisms and other queuing systems
* Using Redis for in-memory DLR tracking and billing
* Advanced message routing/filtering (Simple, Roundrobin, Failover, HLR lookup, Leastcost ...)
* Web and console ui for management
* Supports Unicode (UTF-8) for sending out multilingual SMS
* Supports easy creation and sending of specialized/binary SMS like mono Ringtones, WAP Push, Vcards
* Supports concatenated SMS strings (long SMS)
* Jasmin relies heavily on message queuing through message brokers (Using AMQP), it is designed for performance, high traffic loads and full in-memory execution.

![logo](https://raw.githubusercontent.com/jookies/jasmin/master/misc/doc/sources/_static/jasmin-logo-small.png)

# How to use this image

## Running the daemon

```console
$ docker run -d -p 1401:1401 -p 2775:2775 -p 8990:8990 --name jasmin_01 jookies/jasmin:latest
```

# License

View [license information](https://raw.githubusercontent.com/jookies/jasmin/master/LICENSE) for the software contained in this image.

# Supported Docker versions

This image is officially supported on Docker version 1.9.0.

Support for older versions (down to 1.6) is provided on a best-effort basis.

Please see [the Docker installation documentation](https://docs.docker.com/installation/) for details on how to upgrade your Docker daemon.

# User Feedback

## Documentation

Documentation for using Jasmin's docker image is located [here](http://docs.jasminsms.com/en/latest/installation/index.html#docker).

## Issues and support

If you have any problems with or questions about this image, please contact us through a [GitHub issue](https://github.com/jookies/jasmin/issues).

You can also reach out for support [here](https://groups.google.com/forum/#!forum/jasmin-sms-gateway) or by dropping a message to support [at] jookies [dot] net.
