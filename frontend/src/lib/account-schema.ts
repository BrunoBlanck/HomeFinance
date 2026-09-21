import { z } from 'zod'
import { INSTITUICOES, TIPOS_DE_CONTA } from '@/lib/accounts'
import { keywordsSchema } from '@/lib/keywords'
import { MAX_CENTAVOS } from '@/lib/money'

/** Um schema para o formulário de conta, espelhando o que o servidor cobra.
 *
 *  Validar aqui é conforto de digitação — a autoridade é sempre o backend
 *  (`AGENTS.md`). O que este arquivo garante é que a interface não ofereça um
 *  caminho que o servidor vá recusar: instituição fora da allowlist, dia de
 *  fatura fora de 1–31, ou dia de fatura numa conta que não é cartão.
 *
 *  As duas listas de valores vêm do contrato, não daqui: `TIPOS_DE_CONTA` e
 *  `INSTITUICOES` são derivadas dos `Record<…, string>` de rótulo, que o `tsc`
 *  mantém exaustivos. Valor novo no `openapi.yaml` chega a esta validação
 *  sozinho. */

const DIA_FORA_DA_FAIXA = 'O dia precisa estar entre 1 e 31.'

/** Dia do mês de fechamento/vencimento. `null` é "não configurado", e é um
 *  estado legítimo: a importação então infere as datas pelo arquivo. */
const diaDaFatura = z
  .number()
  .int(DIA_FORA_DA_FAIXA)
  .min(1, DIA_FORA_DA_FAIXA)
  .max(31, DIA_FORA_DA_FAIXA)
  .nullable()

export const accountFormSchema = z
  .object({
    name: z
      .string()
      .trim()
      .min(1, 'Informe um nome.')
      .max(80, 'O nome pode ter no máximo 80 caracteres.'),
    kind: z.enum(TIPOS_DE_CONTA, 'Escolha um tipo de conta.'),
    institution: z.enum(INSTITUICOES, 'Escolha uma instituição da lista.'),
    statementClosingDay: diaDaFatura,
    statementDueDay: diaDaFatura,
    openingBalanceCents: z
      .number()
      .int('Valor fora da faixa permitida.')
      .min(-MAX_CENTAVOS, 'Valor fora da faixa permitida.')
      .max(MAX_CENTAVOS, 'Valor fora da faixa permitida.'),
    openingDate: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, 'Informe uma data válida.'),
    // A lista inteira, sempre (spec 0005 §4.1.3): o formulário é dono dela e o
    // contrato é de substituição — `[]` limpa.
    keywords: keywordsSchema,
  })
  // Fechamento e vencimento só existem em cartão de crédito — mandá-los em
  // outra conta é 422 no servidor. Recusar aqui evita a viagem e, mais
  // importante, documenta na tela por que os campos sumiram quando o tipo
  // mudou.
  .superRefine((valores, ctx) => {
    if (valores.kind === 'credit_card') return
    for (const campo of ['statementClosingDay', 'statementDueDay'] as const) {
      if (valores[campo] === null) continue
      ctx.addIssue({
        code: 'custom',
        path: [campo],
        message: 'Fechamento e vencimento só existem em cartão de crédito.',
      })
    }
  })

export type AccountFormValues = z.infer<typeof accountFormSchema>
