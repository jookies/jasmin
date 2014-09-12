from setuptools import setup, find_packages
from pip.req import parse_requirements

install_reqs = parse_requirements('install-requirements')
test_reqs = parse_requirements('test-requirements')

# Dynamically calculate the version based on jasmin.RELEASE.
release = __import__('jasmin').get_release()

setup(
    name="jasmin",
    version=release,
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
                ('/etc/jasmin/resource', [
                    'misc/config/resource/amqp0-8.stripped.rabbitmq.xml', 
                    'misc/config/resource/amqp0-9-1.xml'],),
                ('/etc/init.d', ['misc/init-script/jasmin']),
                ('/etc/jasmin/store', []),
                ('/var/run/jasmin', []),
                ('/var/log/jasmin', [])],
)
