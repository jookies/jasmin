import sys
import os
import pwd
from setuptools import setup, find_packages
from pip.req import parse_requirements

RUNTIME_USER = 'jasmin'
install_reqs = parse_requirements('install-requirements')
test_reqs = parse_requirements('test-requirements')

# Pre-install checklist
if "install" in sys.argv:
    try:
        # (1) RUNTIME_USER must already exist
        pwnam = pwd.getpwnam(RUNTIME_USER)
    except KeyError:
        print 'Pre-install checklist error:'
        print 'User %s does not exist, will be created ..' % RUNTIME_USER
        print 'Installation cancelled'
        sys.exit(1)

setup(
    name="jasmin",
    version="0.4.0-alpha",
    author="Fourat ZOUARI",
    author_email="fourat@gmail.com",
    url="https://github.com/jookies/jasmin",
    license="Apache License, Version 2.0 (http://www.apache.org/licenses/LICENSE-2.0)",
    description=('Jasmin is a very complete open source SMS Gateway '
                 'with many enterprise-class features.'),
    long_description=open('README.rst', 'r').read(),
    keywords = ['jasmin', 'sms', 'messaging', 'smpp'],
    packages=find_packages(),
    include_package_data=True,
    install_requires=[str(ir.req) for ir in install_reqs],
    tests_require=[str(ir.req) for ir in test_reqs],
    classifiers=[
        'Development Status :: 3 - Alpha',
        'Framework :: Twisted',
        'Intended Audience :: Developers',
        'License :: OSI Approved :: Apache Software License',
        'Operating System :: POSIX',
        'Programming Language :: Python',
        'Topic :: Software Development :: Libraries :: Python Modules',
        'Topic :: System :: Networking',
        'Topic :: Communications',
        'Topic :: Communications :: Telephony',
    ],
    platforms='POSIX',
    data_files=[('/etc/jasmin', ['misc/config/jasmin.cfg']),
                ('/etc/jasmin/resource', ['misc/config/resource/amqp0-8.stripped.rabbitmq.xml', 'misc/config/resource/amqp0-9-1.xml']),
                ('/etc/jasmin/store', []),
                ('/var/log/jasmin', [])],
)

def rchown(path, uid, gid):
    "Will recursively chown path"
    os.chown(path, uid, gid)
    for item in os.listdir(path):
        itempath = os.path.join(path, item)
        if os.path.isfile(itempath):
            os.chown(itempath, uid, gid)
        elif os.path.isdir(itempath):
            os.chown(itempath, uid, gid)
            rchown(itempath, uid, gid)

# Post-install actions
if "install" in sys.argv:
    # (1) data_files must be owned by the RUNTIME_USER
    data_file_folders = ['/etc/jasmin', '/var/log/jasmin']
    for folder in data_file_folders:
        if os.path.exists(folder):
            rchown(folder, pwnam.pw_uid, pwnam.pw_gid)
