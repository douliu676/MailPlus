type MailContact = {
  raw: string
  name: string
  email: string
}

const emailPattern = /[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/i

function splitContactList(value: string) {
  const parts: string[] = []
  let current = ''
  let inQuotes = false
  let angleDepth = 0

  for (let index = 0; index < value.length; index += 1) {
    const char = value[index]
    const previous = value[index - 1]

    if (char === '"' && previous !== '\\') {
      inQuotes = !inQuotes
    } else if (!inQuotes && char === '<') {
      angleDepth += 1
    } else if (!inQuotes && char === '>' && angleDepth > 0) {
      angleDepth -= 1
    }

    if (!inQuotes && angleDepth === 0 && (char === ',' || char === ';')) {
      if (current.trim()) parts.push(current.trim())
      current = ''
      continue
    }

    current += char
  }

  if (current.trim()) parts.push(current.trim())
  return parts
}

function cleanName(value: string) {
  return value
    .trim()
    .replace(/^["']|["']$/g, '')
    .replace(/\\"/g, '"')
    .replace(/[<>()]+$/g, '')
    .trim()
}

function parseContact(value: string): MailContact {
  const raw = value.trim()
  const angleMatch = raw.match(/^(.*?)<([^<>]+)>$/)
  if (angleMatch) {
    const email = angleMatch[2].match(emailPattern)?.[0] || angleMatch[2].trim()
    return {
      raw,
      name: cleanName(angleMatch[1]),
      email,
    }
  }

  const email = raw.match(emailPattern)?.[0] || ''
  if (!email) {
    return { raw, name: '', email: raw }
  }

  return {
    raw,
    name: cleanName(raw.replace(email, '')),
    email,
  }
}

function parseContacts(value: string) {
  return splitContactList(value || '').map(parseContact).filter((contact) => contact.email || contact.raw)
}

export function mailContactEmails(value: string) {
  return parseContacts(value).map((contact) => contact.email || contact.raw).join(', ')
}

export function mailContactDetail(value: string) {
  return parseContacts(value)
    .map((contact) => {
      if (!contact.email) return contact.raw
      return contact.name ? `${contact.name} <${contact.email}>` : contact.email
    })
    .join(', ')
}
