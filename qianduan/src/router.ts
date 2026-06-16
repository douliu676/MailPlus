import { createRouter, createWebHistory } from 'vue-router'
import { getAuthToken } from './api/session'

const Login = () => import('./login.vue')
const AdminLayout = () => import('./layout/AdminLayout.vue')
const HomePage = () => import('./pages/HomePage.vue')
const DashboardPage = () => import('./pages/DashboardPage.vue')
const UsersPage = () => import('./pages/UsersPage.vue')
const AccountsPage = () => import('./pages/AccountsPage.vue')
const OutlookAccountsPage = () => import('./pages/OutlookAccountsPage.vue')
const ProxySystemPage = () => import('./pages/ProxySystemPage.vue')
const CardKeySystemPage = () => import('./pages/CardKeySystemPage.vue')
const CardKeyLogsPage = () => import('./pages/CardKeyLogsPage.vue')
const PublicMailPage = () => import('./pages/PublicMailPage.vue')
const QuickMailPage = () => import('./pages/QuickMailPage.vue')
const SettingsPage = () => import('./pages/SettingsPage.vue')
const ProfilePage = () => import('./pages/ProfilePage.vue')
const NotFoundPage = () => import('./pages/NotFoundPage.vue')

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/home', meta: { public: true } },
    { path: '/home', component: HomePage, meta: { public: true, title: '首页' } },
    { path: '/imap/mail', redirect: '/imap/mail/' },
    { path: '/imap/mail/', component: QuickMailPage, meta: { public: true, title: 'IMAP 收件', quickMailMode: 'imap' } },
    { path: '/outlook/mail', redirect: '/outlook/mail/' },
    { path: '/outlook/mail/', component: QuickMailPage, meta: { public: true, title: 'Outlook 收件', quickMailMode: 'outlook' } },
    { path: '/mail/keys=:cardKey/all/:email?', component: PublicMailPage, meta: { public: true, title: 'API取件', autoFetch: true } },
    { path: '/mail/keys=:cardKey', component: PublicMailPage, meta: { public: true, title: 'API取件' } },
    { path: '/login', component: Login, meta: { public: true, title: 'Login' } },
    {
      path: '/admin',
      component: AdminLayout,
      children: [
        { path: '', redirect: '/admin/dashboard' },
        { path: 'dashboard', component: DashboardPage, meta: { title: '\u4eea\u8868\u76d8' } },
        { path: 'users', component: UsersPage, meta: { title: '\u7528\u6237\u7ba1\u7406' } },
        { path: 'accounts', component: AccountsPage, meta: { title: 'IMAP邮箱管理' } },
        { path: 'proxy-system', component: ProxySystemPage, meta: { title: '代理系统' } },
        { path: 'card-keys', component: CardKeySystemPage, meta: { title: '卡密系统' } },
        { path: 'card-key-logs', component: CardKeyLogsPage, meta: { title: '卡密日志' } },
        { path: 'outlook-accounts', component: OutlookAccountsPage, meta: { title: '微软邮箱管理' } },
        { path: 'settings', component: SettingsPage, meta: { title: '\u7cfb\u7edf\u8bbe\u7f6e' } },
        { path: 'profile', component: ProfilePage, meta: { title: '\u4e2a\u4eba\u8d44\u6599' } },
      ],
    },
    { path: '/:pathMatch(.*)*', component: NotFoundPage, meta: { public: true, title: '页面不存在' } },
  ],
})

router.beforeEach((to) => {
  const token = getAuthToken()

  if (!to.meta.public && !token) {
    return '/login'
  }

  if (to.path === '/login' && token) {
    return '/admin/dashboard'
  }

  if (to.path === '/login' && Object.keys(to.query).length > 0) {
    return '/login'
  }
})

export default router
