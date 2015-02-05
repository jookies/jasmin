from pip.req import parse_requirements
install_reqs = parse_requirements('install-requirements')

print install_reqs
for x in install_reqs:
	print 'x:', x

install_requires=[str(ir.req) for ir in install_reqs]

print install_requires