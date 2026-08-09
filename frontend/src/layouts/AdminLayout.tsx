import { useTranslation } from 'react-i18next'
import MainLayout from './MainLayout'

export default function AdminLayout() {
  const { t } = useTranslation()
  const crumbs = [
    { name: t('admin') },
    { name: t('dashboard') },
  ]
  return (
    <MainLayout mode="admin" breadcrumb={crumbs}>
      {/* 实际内容通过 Outlet 由 MainLayout 渲染 */}
    </MainLayout>
  )
}
