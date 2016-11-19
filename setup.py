import getpass
import grp
import os
import pwd
import sys
import uuid

from pip.req import parse_requirements
from setuptools import setup, find_packages

# After passing on travis docker-based ci, sudo is no more
# used, ROOT_PATH is a env variable set in .travis.yml to avoid
# installing things system wide.
ROOT_PATH = os.getenv('ROOT_PATH', '/')

# Pre-installation checklist
if "install" in sys.argv:
    # 1. Check if jasmin user and group were created
    try:
        pwd.getpwnam('jasmin')
        grp.getgrnam('jasmin')
    except KeyError:
        print 'WARNING: jasmin user or group not found !'

    # 2. Check if system folders are created
    sysdirs = ['%s/etc/jasmin' % ROOT_PATH,
               '%s/etc/jasmin/resource' % ROOT_PATH,
               '%s/etc/jasmin/store' % ROOT_PATH,
               '%s/var/log/jasmin' % ROOT_PATH, ]
    for sysdir in sysdirs:
        if not os.path.exists(sysdir):
            print 'WARNING: %s does not exist !' % sysdir

    # 3. Check for permission to write jasmin.cfg in /etc/jasmin/store
    if not os.access('%s/etc/jasmin/store' % ROOT_PATH, os.W_OK):
        print 'WARNING: %s/etc/jasmin/store must be writeable by the current user (%s)' % (
            ROOT_PATH, getpass.getuser())

    # 4. Check if sysdirs are owned by jasmin user
    for sysdir in sysdirs[3:]:
        if os.path.exists(sysdir) and pwd.getpwuid(os.stat(sysdir).st_uid).pw_name != 'jasmin':
            print 'WARNING: %s is not owned by jasmin user !' % sysdir

session = uuid.uuid1()
install_reqs = parse_requirements('install-requirements', session=session)
test_reqs = parse_requirements('test-requirements', session=session)

# Dynamically calculate the version based on jasmin.RELEASE.
release = __import__('jasmin').get_release()

setup(
    name="jasmin",
    version=release,
    author="Jookies LTD",
    author_email="jasmin@jookies.net",
    url="http://www.jasminsms.com",
    license="Apache v2.0",
    description=('Jasmin is a very complete open source SMS Gateway '
                 'with many enterprise-class features.'),
    long_description=open('README.rst', 'r').read(),
    keywords=['jasmin', 'sms', 'messaging', 'smpp', 'smsc', 'smsgateway'],
    packages=find_packages(),
    scripts=['jasmin/bin/jasmind.py', 'jasmin/bin/interceptord.py', 'jasmin/bin/dlrd.py', 'jasmin/bin/dlrlookupd.py'],
    include_package_data=True,
    install_requires=[str(ir.req) for ir in install_reqs],
    tests_require=[str(ir.req) for ir in test_reqs],
    classifiers=[
        'Development Status :: 4 - Beta',
        'Framework :: Twisted',
        'Intended Audience :: Developers',
        'License :: OSI Approved :: Apache Software License',
        'Operating System :: POSIX',
        'Programming Language :: Python :: 2 :: Only',
        'Programming Language :: Python :: 2.7',
        'Topic :: Software Development :: Libraries :: Python Modules',
        'Topic :: System :: Networking',
        'Topic :: Communications',
        'Topic :: Communications :: Telephony',
    ],
    platforms='POSIX',
    data_files=[
        ('%s/etc/jasmin' % ROOT_PATH, ['misc/config/jasmin.cfg']),
        ('%s/etc/jasmin/resource' % ROOT_PATH, [
            'misc/config/resource/amqp0-8.stripped.rabbitmq.xml',
            'misc/config/resource/amqp0-9-1.xml'
        ],),
        ('%s/etc/jasmin/store' % ROOT_PATH, []),
    ],
)
