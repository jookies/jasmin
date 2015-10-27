class Singleton(type):
	_instances = {}

	def __call__(mcs, *args, **kwargs):
		if mcs not in mcs._instances:
			mcs._instances[mcs] = super(Singleton, mcs).__call__(*args, **kwargs)
		return mcs._instances[mcs]
