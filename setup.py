from setuptools import setup, find_packages

def parse_requirements(filename):
    """load requirements from a pip requirements file"""
    lineiter = (line.strip() for line in open(filename))
    return [line for line in lineiter if line and (not line.startswith("#") and not line.startswith('-'))]

setup(
    name="jasmin",
    version='0.10.8',
    author="Jookies LTD",
    author_email="jasmin@jookies.net",
    url="https://www.jasminsms.com",
    license="Apache v2.0",
    description=('Jasmin is a very complete open source SMS Gateway '
                 'with many enterprise-class features.'),
    long_description=open('README.rst', 'r').read(),
    keywords=['jasmin', 'sms', 'messaging', 'smpp', 'smsc', 'smsgateway'],
    packages=find_packages(exclude=["tests"]),
    scripts=['jasmin/bin/jasmind.py', 'jasmin/bin/interceptord.py', 'jasmin/bin/dlrd.py', 'jasmin/bin/dlrlookupd.py'],
    include_package_data=True,
    install_requires=parse_requirements('requirements.txt'),
    tests_require=parse_requirements('requirements-test.txt'),
    classifiers=[
        'Development Status :: 5 - Production/Stable',
        'Framework :: Twisted',
        'Intended Audience :: Developers',
        'License :: OSI Approved :: Apache Software License',
        'Operating System :: POSIX',
        'Programming Language :: Python :: 3 :: Only',
        'Programming Language :: Python :: 3',
        'Topic :: Software Development :: Libraries :: Python Modules',
        'Topic :: System :: Networking',
        'Topic :: Communications',
        'Topic :: Communications :: Telephony',
    ],
    platforms='POSIX'
)
