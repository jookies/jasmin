import logging

class WhiteSpaceStrippingFormatter(logging.Formatter):
    def format(self, record):
        return super(WhiteSpaceStrippingFormatter, self).format(record).replace('\n', '').replace('\r', '')
