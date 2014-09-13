"""Variant of cmd2's option parsing mechanism (http://www.assembla.com/wiki/show/python-cmd2)
"""

import re
import pyparsing
import optparse

class OptionParser(optparse.OptionParser):
    def __init__(self, option_class=optparse.Option):
        optparse.OptionParser.__init__(self, add_help_option=False, option_class=option_class)
    
    def error(self, msg):
        """error(msg : string)

        Print a usage message incorporating 'msg' to stderr and exit.
        If you override this in a subclass, it should not return -- it
        should either exit or raise an exception.
        """
        raise optparse.OptParseError(msg)
        
def remaining_args(oldArgs, newArgList):
    '''
    Preserves the spacing originally in the argument after
    the removal of options.
    
    >>> remaining_args('-f bar   bar   cow', ['bar', 'cow'])
    'bar   cow'
    '''
    pattern = r'\s+'.join(re.escape(a) for a in newArgList) + r'\s*$'
    matchObj = re.search(pattern, oldArgs)
    return oldArgs[matchObj.start():]
   
def _attr_get_(obj, attr):
    '''Returns an attribute's value, or None (no error) if undefined.
       Analagous to .get() for dictionaries.  Useful when checking for
       value of options that may not have been defined on a given
       method.'''
    try:
        return getattr(obj, attr)
    except AttributeError:
        return None
    
optparse.Values.get = _attr_get_
    
options_defined = [] # used to distinguish --options from SQL-style --comments

def options(option_list, arg_desc="arg"):
    '''Used as a decorator and passed a list of optparse-style options,
       alters a method to populate its ``opts`` argument from its
       raw text argument.

       Example: transform
       def do_something(self, arg):

       into
       @options([make_option('-q', '--quick', action="store_true",
                 help="Makes things fast")],
                 "source dest")
       def do_something(self, arg, opts):
           if opts.quick:
               self.fast_button = True
       '''
    if not isinstance(option_list, list):
        option_list = [option_list]
    for opt in option_list:
        options_defined.append(pyparsing.Literal(opt.get_opt_string()))
    def option_setup(func):
        optionParser = OptionParser()
        for opt in option_list:
            optionParser.add_option(opt)
        optionParser.set_usage("%s [options] %s" % (func.__name__[3:], arg_desc))
        optionParser._func = func
        def new_func(instance, arg):
            try:
                opts, newArgList = optionParser.parse_args(arg.split())
                newArgs = remaining_args(arg, newArgList)
                arg = newArgs
            except optparse.OptParseError as e:
                instance.sendData(str(e))
                return instance.sendData(optionParser.format_help())
            return func(instance, arg, opts)
        new_func.__doc__ = func.__doc__
        new_func.__extended_doc__ = optionParser.format_help()
        return new_func
    return option_setup