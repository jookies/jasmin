import json
import os
import sys
import uuid
import re
import requests

import jasmin
from .config import *
from .tasks import httpapi_send
from datetime import datetime, timedelta

sys.path.append("%s/vendor" % os.path.dirname(os.path.abspath(jasmin.__file__)))
import falcon


class JasminHttpApiProxy(object):
    """Provides a WS caller for old Jasmin http api"""

    def call_jasmin(self, url, params=None):
        try:
            r = requests.get('%s/%s' % (old_api_uri, url), params=params)
        except requests.exceptions.ConnectionError as e:
            raise falcon.HTTPInternalServerError('Jasmin httpapi connection error',
                                                 'Could not connect to Jasmin http api (%s): %s' % (old_api_uri, e))
        except Exception as e:
            raise falcon.HTTPInternalServerError('Jasmin httpapi unknown error', str(e))
        else:
            return r.status_code, r.content.strip('"')


class JasminRestApi(object):
    """Parent class for all rest api resources"""

    def build_response_from_proxy_result(self, response, result):
        """Make a unified response format for requests going to old Jasmin http api"""

        if result[0] != 200:
            response.body = json.dumps({'message': result[1]})
        else:
            if '{' in result[1]:
                # It's a json result
                response.body = {'data': json.loads(result[1])}
            else:
                response.body = {'data': result[1]}
        response.status = getattr(falcon, 'HTTP_%s' % result[0])

    def decode_request_data(self, request):
        """Decode the request stream and return a valid json"""

        try:
            request_data = request.stream.read()
            params = json.loads(request_data)
        except Exception as e:
            raise falcon.HTTPPreconditionFailed('Cannot parse JSON data',
                                                'Got unparseable json data: %s' % request_data)
        else:
            return params


class PingResource(JasminRestApi, JasminHttpApiProxy):
    def on_get(self, request, response):
        """
        GET /ping request processing

        Note: Ping is used to check Jasmin's http api
        """

        self.build_response_from_proxy_result(response, self.call_jasmin('ping'))


class BalanceResource(JasminRestApi, JasminHttpApiProxy):
    def on_get(self, request, response):
        """
        GET /secure/balance request processing

        Note: Balance is used by user to check his balance
        """
        self.build_response_from_proxy_result(
            response,
            self.call_jasmin(
                'balance',
                params={
                    'username': request.context['username'],
                    'password': request.context['password']
                }
            )
        )


class RateResource(JasminRestApi, JasminHttpApiProxy):
    def on_get(self, request, response):
        """
        GET /secure/rate request processing

        Note: This method will indicate the rate of the message once sent
        """

        request_args = dict(
            self.decode_request_data(request).items() + {
                'username': request.context['username'],
                'password': request.context['password']
            }.items()
        )

        # Convert _ to -
        # Added for compliance with json encoding/decoding constraints on dev env like .Net
        for k, v in request_args.iteritems():
            del (request_args[k])
            request_args[re.sub('_', '-', k)] = v

        self.build_response_from_proxy_result(
            response,
            self.call_jasmin(
                'rate',
                params=request_args
            )
        )


class SendResource(JasminRestApi, JasminHttpApiProxy):
    def on_post(self, request, response):
        """
        POST /secure/send request processing

        Note: Calls Jasmin http api /send resource
        """

        request_args = dict(
            self.decode_request_data(request).items() + {
                'username': request.context['username'],
                'password': request.context['password']
            }.items()
        )

        # Convert _ to -
        # Added for compliance with json encoding/decoding constraints on dev env like .Net
        for k, v in request_args.iteritems():
            del (request_args[k])
            request_args[re.sub('_', '-', k)] = v

        self.build_response_from_proxy_result(
            response,
            self.call_jasmin(
                'send',
                params=request_args
            )
        )


class SendBatchResource(JasminRestApi, JasminHttpApiProxy):
    def parse_schedule_at(self, val):
        """
        Tries to parse the schedule_at parameter and get the datetime value for scheduling
        :param val: schedule_at rest parameter
        :return: countdown in seconds or raising HTTPPreconditionFailed if parsing errored
        """

        if val is None:
            return 0
        else:
            # Do we have a ISO Date format ?
            try:
                schedule_at = datetime.strptime(val, '%Y-%m-%d %H:%M:%S')

                if schedule_at < datetime.now():
                    raise falcon.HTTPPreconditionFailed('Cannot schedule batch in past date',
                                                        "Invalid past date given: %s" % schedule_at)
            except ValueError:
                # Do we have Seconds format ?
                m = re.match("^(\d+)s$", val)
                if not m:
                    raise falcon.HTTPPreconditionFailed('Cannot parse scheduled_at value',
                                                        ("Got unknown format: %s, correct formats are "
                                                         "'YYYY-MM-DD mm:hh:ss' or number of seconds, "
                                                         "c.f. http://docs.jasminsms.com/en/latest/apis/rest") % val)

                return int(m.group(1))
            else:
                return (schedule_at - datetime.now()).total_seconds()

    def on_post(self, request, response):
        """
        POST /secure/sendbatch request processing

        Note: Calls Jasmin http api /send resource
        """

        batch_id = uuid.uuid4()
        params = self.decode_request_data(request)
        config = {'throughput': http_throughput_per_worker, 'smart_qos': smart_qos}

        # Batch scheduling
        countdown = self.parse_schedule_at(params.get('batch_config', {}).get('schedule_at', None))

        message_count = 0
        for _message_params in params.get('messages', {}):
            # Construct message params
            message_params = {'username': request.context['username'], 'password': request.context['password']}
            message_params.update(params.get('globals', {}))
            message_params.update(_message_params)

            # Convert _ to -
            # Added for compliance with json encoding/decoding constraints on dev env like .Net
            for k, v in message_params.iteritems():
                del(message_params[k])
                message_params[re.sub('_', '-', k)] = v

            # Ignore message if these args are not found
            if 'to' not in message_params or ('content' not in message_params and 'hex-content' not in message_params):
                continue

            # Do we have multiple destinations for this message ?
            if isinstance(message_params.get('to', ''), list):
                to_list = message_params.get('to')
                for _to in to_list:
                    message_params['to'] = _to
                    if countdown == 0:
                        httpapi_send.delay(batch_id, params.get('batch_config', {}), message_params, config)
                    else:
                        httpapi_send.apply_async(
                            args=[batch_id, params.get('batch_config', {}), message_params, config],
                            countdown=countdown)
                    message_count += 1
            else:
                if countdown == 0:
                    httpapi_send.delay(batch_id, params.get('batch_config', {}), message_params, config)
                else:
                    httpapi_send.apply_async(
                        args=[batch_id, params.get('batch_config', {}), message_params, config], countdown=countdown)
                message_count += 1

        response.body = {
            'data': {
                "batchId": '%s' % batch_id,
                "messageCount": message_count
            }
        }
        if countdown > 0:
            response.body['data']['scheduled'] = '%ss' % countdown
