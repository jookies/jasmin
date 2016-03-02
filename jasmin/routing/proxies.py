from jasmin.tools.proxies import ConnectedPB
from jasmin.tools.proxies import JasminPBProxy

class RouterPBProxy(JasminPBProxy):
    'This is a proxy to RouterPB perspective broker'

    @ConnectedPB
    def version_release(self):
        return self.pb.callRemote('version_release')

    @ConnectedPB
    def version(self):
        return self.pb.callRemote('version')

    @ConnectedPB
    def persist(self, profile="jcli-prod", scope='all'):
        return self.pb.callRemote('persist', profile, scope)

    @ConnectedPB
    def load(self, profile="jcli-prod", scope='all'):
        return self.pb.callRemote('load', profile, scope)

    @ConnectedPB
    def is_persisted(self):
        return self.pb.callRemote('is_persisted')

    @ConnectedPB
    def user_add(self, user):
        return self.pb.callRemote('user_add', self.pickle(user))

    @ConnectedPB
    def user_authenticate(self, username, password):
        return self.pb.callRemote('user_authenticate', username, password)

    @ConnectedPB
    def user_remove(self, uid):
        return self.pb.callRemote('user_remove', uid)

    @ConnectedPB
    def user_enable(self, uid):
        return self.pb.callRemote('user_enable', uid)

    @ConnectedPB
    def user_disable(self, uid):
        return self.pb.callRemote('user_disable', uid)

    @ConnectedPB
    def user_remove_all(self):
        return self.pb.callRemote('user_remove_all')

    @ConnectedPB
    def user_get_all(self, gid=None):
        return self.pb.callRemote('user_get_all', gid)

    @ConnectedPB
    def user_update_quota(self, uid, cred, quota, value):
        return self.pb.callRemote('user_update_quota', uid, cred, quota, value)

    @ConnectedPB
    def group_add(self, group):
        return self.pb.callRemote('group_add', self.pickle(group))

    @ConnectedPB
    def group_enable(self, gid):
        return self.pb.callRemote('group_enable', gid)

    @ConnectedPB
    def group_disable(self, gid):
        return self.pb.callRemote('group_disable', gid)

    @ConnectedPB
    def group_remove(self, gid):
        return self.pb.callRemote('group_remove', gid)

    @ConnectedPB
    def group_remove_all(self):
        return self.pb.callRemote('group_remove_all')

    @ConnectedPB
    def group_get_all(self):
        return self.pb.callRemote('group_get_all')

    @ConnectedPB
    def mtroute_add(self, route, order):
        return self.pb.callRemote('mtroute_add', self.pickle(route), order)

    @ConnectedPB
    def moroute_add(self, route, order):
        return self.pb.callRemote('moroute_add', self.pickle(route), order)

    @ConnectedPB
    def mtroute_remove(self, order):
        return self.pb.callRemote('mtroute_remove', order)

    @ConnectedPB
    def moroute_remove(self, order):
        return self.pb.callRemote('moroute_remove', order)

    @ConnectedPB
    def mtroute_flush(self):
        return self.pb.callRemote('mtroute_flush')

    @ConnectedPB
    def moroute_flush(self):
        return self.pb.callRemote('moroute_flush')

    @ConnectedPB
    def mtroute_get_all(self):
        return self.pb.callRemote('mtroute_get_all')

    @ConnectedPB
    def moroute_get_all(self):
        return self.pb.callRemote('moroute_get_all')

    @ConnectedPB
    def mtinterceptor_add(self, interceptor, order):
        return self.pb.callRemote('mtinterceptor_add', self.pickle(interceptor), order)

    @ConnectedPB
    def mointerceptor_add(self, interceptor, order):
        return self.pb.callRemote('mointerceptor_add', self.pickle(interceptor), order)

    @ConnectedPB
    def mtinterceptor_remove(self, order):
        return self.pb.callRemote('mtinterceptor_remove', order)

    @ConnectedPB
    def mointerceptor_remove(self, order):
        return self.pb.callRemote('mointerceptor_remove', order)

    @ConnectedPB
    def mtinterceptor_flush(self):
        return self.pb.callRemote('mtinterceptor_flush')

    @ConnectedPB
    def mointerceptor_flush(self):
        return self.pb.callRemote('mointerceptor_flush')

    @ConnectedPB
    def mtinterceptor_get_all(self):
        return self.pb.callRemote('mtinterceptor_get_all')

    @ConnectedPB
    def mointerceptor_get_all(self):
        return self.pb.callRemote('mointerceptor_get_all')
