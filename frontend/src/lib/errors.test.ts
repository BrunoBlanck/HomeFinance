import { describe, expect, it } from 'vitest'
import { ApiError, NetworkError } from '@/api/client'
import {
  isCategoriaRecusada,
  isConflict,
  isImportError,
  isKeywordTaken,
  isUnauthenticated,
  keywordTakenOf,
  MSG_CATEGORIA_RECUSADA,
  messageForError,
} from './errors'

describe('messageForError', () => {
  it('traduz cada faixa de status para a redação do projeto', () => {
    expect(messageForError(new ApiError(400, 'VALIDATION_FAILED', ['email']))).toBe(
      'Confira os dados informados e tente de novo.',
    )
    expect(messageForError(new ApiError(422, null, []))).toBe(
      'Confira os dados informados e tente de novo.',
    )
    expect(messageForError(new ApiError(401, 'UNAUTHENTICATED', []))).toBe(
      'Sua sessão expirou. Entre de novo.',
    )
    expect(messageForError(new ApiError(403, 'FORBIDDEN', []))).toBe(
      'Você não tem acesso a este conteúdo.',
    )
    expect(messageForError(new ApiError(404, 'NOT_FOUND', []))).toBe('Não encontramos esta página.')
    expect(messageForError(new ApiError(429, 'RATE_LIMITED', []))).toBe(
      'Muitas tentativas. Aguarde alguns minutos e tente de novo.',
    )
    expect(messageForError(new ApiError(500, 'INTERNAL_ERROR', []))).toBe(
      'Algo falhou do nosso lado. Tente de novo em instantes.',
    )
    expect(messageForError(new NetworkError())).toBe(
      'Sem conexão com o servidor. Verifique sua internet.',
    )
  })

  it('nunca repassa texto cru: erro desconhecido cai na mensagem genérica', () => {
    expect(messageForError(new Error('database password is hunter2'))).toBe(
      'Algo falhou do nosso lado. Tente de novo em instantes.',
    )
  })

  it('dá a cada código da importação uma redação própria', () => {
    // O ponto do teste não é o texto: é que os seis códigos NÃO caem na
    // mensagem genérica de 422. Cada um leva a uma ação diferente da tela.
    const generica = 'Confira os dados informados e tente de novo.'
    const codigos = [
      'IMPORT_PASSWORD_REQUIRED',
      'IMPORT_PASSWORD_INVALID',
      'IMPORT_FORMAT_UNKNOWN',
      'IMPORT_FORMAT_AMBIGUOUS',
      'IMPORT_TARGET_MISMATCH',
      'IMPORT_FILE_REJECTED',
    ] as const

    const vistas = new Set<string>()
    for (const codigo of codigos) {
      const texto = messageForError(new ApiError(422, codigo, []))
      expect(texto).not.toBe(generica)
      vistas.add(texto)
    }
    expect(vistas.size).toBe(codigos.length)
  })

  it('reconhece erro da importação', () => {
    expect(isImportError(new ApiError(422, 'IMPORT_PASSWORD_REQUIRED', []))).toBe(true)
    expect(isImportError(new ApiError(422, 'VALIDATION_FAILED', []))).toBe(false)
    expect(isImportError(new NetworkError())).toBe(false)
  })

  it('409 CONFLICT tem frase própria, escolhida pelo CÓDIGO e não pelo status', () => {
    // Um 409 sem código (ou com outro código) cai no genérico: é o código que
    // diz que a ação da tela é "conferir a prévia de novo".
    expect(messageForError(new ApiError(409, 'CONFLICT', []))).toBe(
      'Os dados mudaram enquanto a operação rodava. Confira a prévia de novo.',
    )
    expect(messageForError(new ApiError(409, null, []))).toBe(
      'Algo falhou do nosso lado. Tente de novo em instantes.',
    )
    expect(messageForError(new ApiError(409, 'RESOURCE_IN_USE', []))).toBe(
      'Algo falhou do nosso lado. Tente de novo em instantes.',
    )
  })

  it('reconhece o conflito de escrita em lote', () => {
    expect(isConflict(new ApiError(409, 'CONFLICT', []))).toBe(true)
    expect(isConflict(new ApiError(409, 'KEYWORD_TAKEN', []))).toBe(false)
    expect(isConflict(new ApiError(409, null, []))).toBe(false)
    expect(isConflict(new NetworkError())).toBe(false)
  })

  it('reconhece sessão expirada', () => {
    expect(isUnauthenticated(new ApiError(401, 'UNAUTHENTICATED', []))).toBe(true)
    expect(isUnauthenticated(new ApiError(403, 'FORBIDDEN', []))).toBe(false)
    expect(isUnauthenticated(new NetworkError())).toBe(false)
  })

  describe('palavra-chave já usada (409 KEYWORD_TAKEN)', () => {
    const ownerId = '33333333-3333-4333-8333-333333333333'

    it('reconhece pelo código e lê a palavra e a dona', () => {
      const erro = new ApiError(409, 'KEYWORD_TAKEN', ['keyword', 'ownerId'], {
        keyword: 'padaria',
        ownerId,
      })
      expect(isKeywordTaken(erro)).toBe(true)
      expect(keywordTakenOf(erro)).toEqual({ keyword: 'padaria', ownerId })
    })

    // A corrida rara em que o índice único decide: o servidor sabe a palavra
    // mas não quem a tem. A tela ainda precisa da palavra para marcar a ficha.
    it('aceita o 409 sem ownerId', () => {
      const erro = new ApiError(409, 'KEYWORD_TAKEN', ['keyword'], { keyword: 'nubank' })
      expect(isKeywordTaken(erro)).toBe(true)
      expect(keywordTakenOf(erro)).toEqual({ keyword: 'nubank', ownerId: undefined })
    })

    it('não confunde com outro 409 nem com um 400 de forma', () => {
      expect(isKeywordTaken(new ApiError(409, 'RESOURCE_IN_USE', []))).toBe(false)
      expect(isKeywordTaken(new ApiError(400, 'VALIDATION_FAILED', ['keywords[0]']))).toBe(false)
      // Código certo mas sem a palavra: não há ficha a marcar, então não é
      // tratado como o caso especial — cai na mensagem genérica.
      expect(isKeywordTaken(new ApiError(409, 'KEYWORD_TAKEN', []))).toBe(false)
      expect(isKeywordTaken(new NetworkError())).toBe(false)
      expect(keywordTakenOf(new NetworkError())).toBeNull()
    })
  })

  describe('categoria recusada (422 em fields.categoryId, spec 0005 §13)', () => {
    // A prosa que o servidor manda no campo nunca chega à tela: a frase é a
    // nossa, escolhida pelo STATUS e pelo NOME do campo.
    const doServidor = { categoryId: 'Este grupo tem subcategorias. Escolha uma subcategoria.' }

    it('reconhece o 422 que aponta categoryId', () => {
      const erro = new ApiError(422, 'VALIDATION_FAILED', ['categoryId'], doServidor)
      expect(isCategoriaRecusada(erro)).toBe(true)
      expect(messageForError(erro)).toBe(MSG_CATEGORIA_RECUSADA)
      expect(MSG_CATEGORIA_RECUSADA).toBe(
        'Esta categoria não pode receber este lançamento. Escolha outra na lista.',
      )
      // A frase NÃO nomeia a causa: as três (grupo com subcategorias, arquivada,
      // natureza trocada) chegam pelo mesmo campo, e nomear uma mentiria nas outras.
    })

    // Vale igual para os três caminhos de escrita, inclusive quando o texto do
    // servidor é outro: quem escolhe a frase é a tela.
    it('não depende do texto que veio no campo', () => {
      const erro = new ApiError(422, 'VALIDATION_FAILED', ['categoryId'], {
        categoryId: 'qualquer coisa',
      })
      expect(messageForError(erro)).toBe(MSG_CATEGORIA_RECUSADA)
    })

    it('não confunde com o 400 de forma nem com 422 em outro campo', () => {
      // 400: `categoryId` ausente ou fora da forma de UUID — outro problema,
      // outra frase.
      expect(
        isCategoriaRecusada(new ApiError(400, 'VALIDATION_FAILED', ['categoryId'], doServidor)),
      ).toBe(false)
      // 422 na perna de transferência aponta `id`, não `categoryId`.
      expect(
        isCategoriaRecusada(
          new ApiError(422, 'VALIDATION_FAILED', ['id'], {
            id: 'Transferência não tem categoria.',
          }),
        ),
      ).toBe(false)
      expect(isCategoriaRecusada(new ApiError(422, 'VALIDATION_FAILED', []))).toBe(false)
      expect(isCategoriaRecusada(new NetworkError())).toBe(false)
      // E o 422 sem campo continua na frase genérica.
      expect(messageForError(new ApiError(422, 'VALIDATION_FAILED', []))).toBe(
        'Confira os dados informados e tente de novo.',
      )
    })
  })
})
