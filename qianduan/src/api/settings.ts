export type PublicSettings = {
  site_name?: string
  site_logo?: string
  site_subtitle?: string
  table_default_page_size?: number
  table_page_size_options?: number[]
}

export async function getPublicSettings(): Promise<PublicSettings> {
  const response = await fetch('/api/settings/public')

  if (!response.ok) {
    throw new Error('Failed to fetch public settings')
  }

  const result = await response.json()
  return result.data ?? result
}
